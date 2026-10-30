# Music by voice: Home Assistant + Music Assistant

How to make the speakers play music by voice: a **Music Assistant (MA)** server, the speakers as
Sendspin players, the HA integration, and the scripts/automations that turn «Алиса, включи группу
Кино» into music in the right room. The speaker side (sendspin-host, shared mixer, ducking, wake word
over music) is in [music-sendspin.md](../music-sendspin.md); the voice assistant itself is in
[ha-assistant-setup.md](ha-assistant-setup.md). Templates: [`../hass/music/`](../../hass/music/).

```
«включи группу Кино» ─► speaker ─(Wyoming)─► HA Assist ─► sentence trigger / LLM tool
                                                           └► script: MA search → play on the room's player
MA server ──(Sendspin)──► speaker (sendspin-host) ──► the speaker's dmix
```

The phrases are Russian (that is what the speakers speak); adapt them for another language.

## 1. Music Assistant server

- Run MA as its own container, e.g. `ghcr.io/music-assistant/server:<version>`, pinned. **MA ≥ 2.10.7**
  is needed: its Sendspin speaks the 1.0 protocol the speaker's host uses.
- **`network_mode: host`.** Spotify's Soloist engine does not work behind a Docker bridge, and mDNS
  player discovery needs it too.
- Ports on `<ma-host>`: **8095** web UI + API (HA connects here), **8097** stream server, **8927**
  Sendspin server.
- Keep it out of the HA container: MA updates often and Sendspin is still a technical preview; a bad
  MA release shouldn't share HA's blast radius.
- An HTTPS reverse proxy in front of 8095 is handy: the HA integration's login is a browser step.

### Spotify

- Engine **Soloist** (Spotify's official headless Connect client, downloaded by MA; needs a Premium
  account). It ran fine on a CPU without AVX.
- Set **your own Spotify Client ID** in the provider (create an app in the Spotify developer dashboard
  and add MA's redirect URI to it). With MA's shared ID the first library sync hits Spotify's rate
  limiter and retries forever.
- Spotify blocks some countries; MA's egress has to reach Spotify from a supported region.
- **Liked Songs** appear in MA as a synthetic playlist. Open it in the MA UI and copy its URI —
  something like `spotify--<instance>://playlist/liked_songs-spotify--<instance>`. That is your
  `<LIKED_SONGS_URI>`.

## 2. The speakers as players

Turn the player on (`sendspin = true`; it is off by default) and point each speaker at MA
(`sendspin_server = ws://<ma-host>:8927/sendspin`, see
[music-sendspin.md](../music-sendspin.md#connecting-to-music-assistant)) and give it the same
`sendspin_name` as its name in HA. On a weak Wi-Fi link, set the player's *Preferred Sendspin
format* in MA to FLAC/Opus to cut bandwidth.

## 3. HA integration

- *Settings → Devices & services → Add integration → Music Assistant*, URL of the MA server.
- MA's players merge into the existing speaker devices when the names match, so they inherit the
  speakers' **areas**. Check that every player has the right area: the room logic below depends on it.
- Expose the speakers' MA `media_player`s to Assist. Don't expose other players in the same room
  (e.g. a TV box). Two players in one area make «пауза» ambiguous.
- `<MA_CONFIG_ENTRY_ID>`: the entry id of the Music Assistant integration (it is in the URL of the
  integration's page, or `config_entries/get` over the websocket API).

## 4. Voice: what goes where

| Phrase | Handled by | Template |
|---|---|---|
| «пауза / стоп / хватит», «продолжи», «дальше», «назад», «громче / тише» | built-in media intents via custom sentences | [`custom_sentences_media_ru.yaml`](../../hass/music/custom_sentences_media_ru.yaml) |
| «включи исполнителя / группу / альбом / песню / трек / плейлист / радио …», «играй / сыграй / хочу послушать …», «включи музыку …», «включи мои лайки / любимое / мою любимую музыку / музыку» | automation **Music: by voice** (no LLM) | [`music_by_voice.yaml`](../../hass/music/music_by_voice.yaml) |
| other explicit music requests, e.g. «Алиса, поставь что-нибудь из Металлики» | the LLM agent → script tool **«Включить музыку»** | [`play_music.yaml`](../../hass/music/play_music.yaml) |
| «выключи», «выруби», «выключи / останови музыку» | automation **Music: stop** | [`music_stop.yaml`](../../hass/music/music_stop.yaml) |
| (all of the above that play something) | background worker **Music: find and play** | [`music_find_and_play.yaml`](../../hass/music/music_find_and_play.yaml) |

Setup:
1. Replace the placeholders in the templates: `<MA_CONFIG_ENTRY_ID>`, `<LIKED_SONGS_URI>`,
   `<DEFAULT_AREA_ID>` (where to play if the room can't be determined).
2. Create the two scripts first (ids `music_find_and_play`, `play_music`), then the two automations.
   Paste each into the UI's YAML editor, or POST it to `/api/config/<script|automation>/config/<id>`.
3. Expose **only** `script.play_music` to Assist (the LLM's tool). Keep the worker and the automations
   unexposed.
4. Copy the custom sentences file, then `conversation.reload` with `{"language": "ru"}`.
5. Add the prompt rules below to the LLM agent.

### How the flow works

- The trigger or the tool only starts the worker with `script.turn_on` (non-blocking) and answers at
  once: «Ищу.» («Сейчас.» for liked songs). Then the music starts **without a spoken comment**. If
  nothing matches, the room's speaker says «Не нашла …» via `assist_satellite.announce`.
- Room: automations use `area_id(trigger.device_id)` (the speaker that heard the phrase). The LLM tool
  takes a `room` field; HA tells the agent which area its speaker is in.
- The player is the MA `media_player` in that area; the speaker for «не нашла» is the
  `assist_satellite` in that area.
- Search: `music_assistant.search` (well under a second). Picking a result:
  - typed requests (artist / album / track / playlist / radio) take the first result of that type;
  - `any` tries, in order: an exact track name if the query has 2+ words, then an artist whose name
    equals the query after transliteration («металлика» = Metallica, «кино» = Kino), then an exact
    track, then a similar artist (first 4 letters), then a similar track;
  - nothing similar means «не нашла». Spotify always returns *something*, so without the similarity
    check «не нашла» never happens and you get a random artist.
- Artists are played as their top ~20 tracks (a second, track-type search filtered by the artist,
  shuffled) instead of the artist URI. Single tracks start with `radio_mode` so music continues after
  them.

### Prompt rules for the LLM agent

Add to the agent's prompt (we run them in Russian; the meaning is what matters):

- Play music **only when the request is clearly about music**: the words "music / song / track /
  artist / band / album / playlist / radio / my likes", or the verbs "play / I want to listen". Then
  call the script «Включить музыку». The room is the one with the speaker you are talking to unless
  another is named. Artist names are often mis-recognised, foreign ones especially: fix them by sound
  and write foreign names in Latin script. "My favourite", "my likes", plain "music" = type `likes`.
  Don't use `HassMediaSearchAndPlay`.
- A bare "turn on <X>" with no music cue, where no such device, scene or mode exists, is **not**
  music: say there is no such device.
- After calling «Включить музыку», answer with exactly one word: "Searching." Say nothing else: the
  music starts by itself, and the speaker reports if nothing was found.
- Never turn speakers or media players off with `HassTurnOff`: "turn off / stop" about music means
  `HassMediaPause`.

## 5. Gotchas (each one was observed)

- **Playlist search goes to Spotify's global catalogue.** «включи мои лайки» via search found a
  stranger's public playlist; even "Liked Songs <your name>" returns someone else's. Use the fixed
  Liked Songs URI.
- **An artist URI makes MA fetch the whole discography** before playing (36 s for a big artist that is
  not in your library). Hence the top-tracks approach. Large playlists take a while too (MA fetches
  their tracks first).
- **HA's `HassMediaSearchAndPlay` returns only after playback started**, so the agent's answer comes
  after a long silence and is spoken over the music. Telling the LLM to say «Ищу…» before the tool
  call doesn't help: HA streams the reply to TTS only after enough text, so a short pre-tool word is
  spoken at the end together with the rest. Hence the background worker.
- **Sentence triggers win over intents.** HA's default agent checks automation sentence triggers
  before built-in intents and custom sentences, so the automations shadow any overlapping custom
  sentence. Remove duplicates from the custom sentences to keep one source of truth.
- **Bare «включи X» as music is too risky.** It hijacks device names the matcher missed. Music needs
  an explicit cue (see the prompt rules); the automations only match explicit phrases.
- **MA players don't support `turn_off`.** An LLM mapping «выключи» to `HassTurnOff` gets a service
  error and improvises an excuse. Hence Music: stop and the prompt rule.
- **Cancelling a voice run doesn't stop its automation.** If the speaker drops a slow run, the
  started script still finishes (see [voice-slow-replies.md](../voice-slow-replies.md)); the music will
  still start.
- The STT model mangles foreign artist names. The LLM path fixes many of them by sound; the no-LLM
  path searches what was heard, and then the similarity check can return «не нашла».

## 6. Troubleshooting

| Symptom | Look at |
|---|---|
| «Ищу.» but nothing plays and no «не нашла» | the worker's trace (*Settings → Scripts → Music: find and play → Traces*): no MA player in the speaker's area, or `play_media` failed |
| Music plays in the wrong room | the areas of the speaker devices and of the MA players |
| A random artist plays | the request went through the LLM with a mis-heard name; check the transcript in the Assist debug view |
| Long delay before music | MA resolving a big playlist or album; artists should be fast (top tracks) |
| Choppy playback on one speaker | Wi-Fi; Preferred Sendspin format FLAC/Opus; see [music-sendspin.md](../music-sendspin.md) |
