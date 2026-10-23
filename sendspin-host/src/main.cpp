// sendspin-host — a Sendspin (Music Assistant) player for the speaker, run by yastationd as a child
// process, the way spotter-host is. It listens on ws://0.0.0.0:<port>/sendspin for the server to
// connect (and can also dial one, -u), decodes PCM/FLAC with sendspin-cpp, and plays it through
// the device's dmix PCM, so music mixes with yastationd's own voice output.
//
//   sendspin-host -n NAME -s STATE_FILE [-p PORT] [-D PCM] [-u URL] [-a] [-V VOL] [-G] [-l LEVEL]
//
//   -n NAME        friendly name shown in Music Assistant
//   -s STATE_FILE  identity + pairing records (the static keypair, i.e. the stable client_id)
//   -p PORT        listen port (default 8928)
//   -D PCM         ALSA playback PCM (default "default" = the stock plug -> dmix chain)
//   -u URL         also connect to this server, e.g. ws://<ma-host>:8927/sendspin
//   -a             unpaired access: an unpaired server may play (LAN-only devices, see README)
//   -V VOL         player volume 0..100 announced to the server at start (default 100)
//   -G             the server's volume is not applied as a gain here: yastationd maps it onto the
//                  speaker's master volume instead ("linked" volume); mute still silences music
//   -l LEVEL       sendspin-cpp log level: none, error, warn, info (default), debug, verbose
//
// stdin, one command per line (from yastationd):
//   duck <0..1>    music level while the assistant talks (1 = normal), ramped
//   volume <0..100>  the speaker's volume changed locally: report it to the server (linked mode)
// EOF on stdin ends the process.
// stdout, one event per line (to yastationd):
//   READY <client_id>
//   CONNECTED 0|1          a Sendspin server is connected
//   STREAM start <rate> <channels> <bits> | STREAM end
//   VOLUME <0..100> <muted 0|1>   the server's player volume
// Logs go to stderr.
#include <getopt.h>
#include <poll.h>
#include <unistd.h>

#include <algorithm>
#include <atomic>
#include <chrono>
#include <csignal>
#include <cstdarg>
#include <cstdio>
#include <cstdlib>
#include <cstring>
#include <mutex>
#include <string>
#include <thread>

#include "alsa_sink.h"
#include "file_persistence_provider.h"
#include "log.h"
#include "sendspin/client.h"
#include "sendspin/player_role.h"

using namespace sendspin;

namespace {

std::atomic<bool> running{true};
std::atomic<int> local_volume{-1};  // from stdin, applied on the main loop (-1 = nothing pending)
std::mutex out_mu;

void emit(const char *fmt, ...) {
  std::lock_guard<std::mutex> lk(out_mu);
  va_list ap;
  va_start(ap, fmt);
  std::vfprintf(stdout, fmt, ap);
  va_end(ap);
  std::fputc('\n', stdout);
  std::fflush(stdout);
}

void on_signal(int) { running.store(false); }

bool parse_level(const char *s, LogLevel &l) {
  static const struct {
    const char *n;
    LogLevel l;
  } m[] = {{"none", LogLevel::NONE},   {"error", LogLevel::ERROR}, {"warn", LogLevel::WARN},
           {"info", LogLevel::INFO},   {"debug", LogLevel::DEBUG}, {"verbose", LogLevel::VERBOSE}};
  for (auto &e : m)
    if (std::strcmp(s, e.n) == 0) return l = e.l, true;
  return false;
}

struct Listener : PlayerRoleListener {
  AlsaSink &sink;
  PlayerRole *player = nullptr;
  bool volume_gain = true;  // false with -G
  explicit Listener(AlsaSink &s) : sink(s) {}

  size_t on_audio_write(uint8_t *data, size_t length, uint32_t timeout_ms) override {
    return sink.write(data, length, timeout_ms);
  }
  void on_stream_start() override {
    auto &p = player->get_current_stream_params();
    if (!p.is_complete()) {
      LOGW("stream start without complete parameters");
      return;
    }
    sink.configure(*p.sample_rate, *p.channels, *p.bit_depth);
    emit("STREAM start %u %u %u", unsigned(*p.sample_rate), unsigned(*p.channels), unsigned(*p.bit_depth));
  }
  void on_stream_end() override {
    sink.release();
    emit("STREAM end");
  }
  void on_volume_changed(uint8_t v) override { report(); }
  void on_mute_changed(bool) override { report(); }
  void on_output_delay_changed(uint16_t ms) override { LOGI("output delay %u ms", unsigned(ms)); }
  void report() {
    sink.set_volume(volume_gain ? player->get_volume() : 100, player->get_muted());
    emit("VOLUME %u %d", unsigned(player->get_volume()), player->get_muted() ? 1 : 0);
  }
};

struct ClientEvents : SendspinClientListener {
  void on_trust_changed(ConnectionTrust t) override {
    LOGI("server trust: %s", t == ConnectionTrust::USER ? "paired" : "unpaired");
  }
  void on_pairing_succeeded(const std::string &id) override { LOGI("paired with %s", id.c_str()); }
  void on_display_pairing_code(const std::string &code, SendspinPairingCodeFormat) override {
    LOGI("pairing code: %s", code.c_str());
  }
};

struct Network : SendspinNetworkProvider {
  bool is_network_ready() override { return true; }  // the server socket binds 0.0.0.0
};

// stdin reader: commands from yastationd. Runs on its own thread; the sink's setters are atomic.
void read_commands(AlsaSink &sink) {
  char line[256];
  while (running.load() && std::fgets(line, sizeof line, stdin)) {
    float f;
    int v;
    if (std::sscanf(line, "duck %f", &f) == 1) sink.set_duck(f);
    else if (std::sscanf(line, "volume %d", &v) == 1) local_volume.store(std::clamp(v, 0, 100));
    else LOGW("unknown command: %s", line);
  }
  running.store(false);  // EOF: our parent is gone
}

void usage(const char *prog) {
  std::fprintf(stderr, "usage: %s -n NAME -s STATE_FILE [-p PORT] [-D PCM] [-u URL] [-a] [-V VOL] [-G] [-l LEVEL]\n",
               prog);
}

}  // namespace

int main(int argc, char **argv) {
  std::signal(SIGINT, on_signal);
  std::signal(SIGTERM, on_signal);
  std::signal(SIGPIPE, SIG_IGN);

  std::string name, state, url, pcm = "default";
  uint16_t port = SendspinClientConfig::DEFAULT_SERVER_PORT;
  bool unpaired = false, volume_gain = true;
  int volume = 100;
  LogLevel level = LogLevel::INFO;
  for (int c; (c = getopt(argc, argv, "n:s:p:D:u:aV:Gl:h")) != -1;) {
    switch (c) {
      case 'n': name = optarg; break;
      case 's': state = optarg; break;
      case 'p': port = uint16_t(std::atoi(optarg)); break;
      case 'D': pcm = optarg; break;
      case 'u': url = optarg; break;
      case 'a': unpaired = true; break;
      case 'V': volume = std::clamp(std::atoi(optarg), 0, 100); break;
      case 'G': volume_gain = false; break;
      case 'l':
        if (!parse_level(optarg, level)) return usage(argv[0]), 2;
        break;
      default: return usage(argv[0]), c == 'h' ? 0 : 2;
    }
  }
  if (name.empty() || state.empty() || port == 0) return usage(argv[0]), 2;
  SendspinClient::set_log_level(level);

  SendspinClientConfig cfg;
  cfg.name = name;
  cfg.product_name = "yastationd";
  cfg.manufacturer = "yastationd";
  cfg.software_version = SENDSPIN_HOST_VERSION;
  cfg.server_port = port;
  // No display for a pairing code: servers come in through unpaired access (-a) for now.

  AlsaSink sink;
  sink.pcm = pcm;
  FilePersistenceProvider persistence(state);
  Network network;
  ClientEvents events;

  SendspinClient client(std::move(cfg));
  client.set_persistence_provider(&persistence);
  client.set_network_provider(&network);
  client.set_listener(&events);
  client.set_unpaired_access_enabled(unpaired);

  PlayerRoleConfig pc;
  pc.audio_formats = {
      {SendspinCodecFormat::FLAC, 2, 48000, 16},
      {SendspinCodecFormat::FLAC, 2, 44100, 16},
      {SendspinCodecFormat::PCM, 2, 48000, 16},
      {SendspinCodecFormat::PCM, 2, 44100, 16},
  };
  auto &player = client.add_player(std::move(pc));
  player.set_output_delay_adjustable(true);
  player.set_volume_adjustable(true);
  player.set_mute_adjustable(true);
  // Announce a real starting volume: the library's default is 0, which a server adopts as the
  // player's volume (Music Assistant then shows 0 while the speaker plays at its own level).
  player.update_volume(uint8_t(volume));
  Listener listener(sink);
  listener.player = &player;
  listener.volume_gain = volume_gain;
  player.set_listener(&listener);
  sink.on_played = [&player](uint32_t frames, int64_t finish_us) { player.notify_audio_played(frames, finish_us); };

  if (!client.start()) {
    LOGE("start failed (port %u busy?)", unsigned(port));
    return 1;
  }
  sink.set_volume(volume_gain ? player.get_volume() : 100, player.get_muted());
  LOGI("listening on :%u/sendspin as \"%s\", unpaired access %s", unsigned(port), name.c_str(),
       unpaired ? "on" : "off");
  emit("READY %s", client.client_id().c_str());
  // connect_to() is one attempt: the library does not redial. Redial while disconnected (no
  // network yet at boot, the server restarting, a Wi-Fi drop), backing off 5 s -> 60 s.
  constexpr int64_t kRedialMinUs = 5000000, kRedialMaxUs = 60000000;
  int64_t redial_us = kRedialMinUs, next_dial_us = 0;
  if (!url.empty()) {
    client.connect_to(url);
    next_dial_us = SendspinClient::now_us() + redial_us;
  }

  std::thread(read_commands, std::ref(sink)).detach();
  bool connected = false;
  emit("CONNECTED 0");
  while (running.load()) {
    client.loop();
    if (int v = local_volume.exchange(-1); v >= 0 && v != player.get_volume()) player.update_volume(uint8_t(v));
    if (client.is_connected() != connected) {
      connected = !connected;
      emit("CONNECTED %d", connected ? 1 : 0);
      redial_us = kRedialMinUs;
    }
    if (!url.empty() && !connected && SendspinClient::now_us() >= next_dial_us) {
      client.connect_to(url);
      next_dial_us = SendspinClient::now_us() + redial_us;
      redial_us = std::min(redial_us * 2, kRedialMaxUs);
    } else if (connected) {
      next_dial_us = SendspinClient::now_us() + redial_us;  // after a drop, the first redial waits
    }
    std::this_thread::sleep_for(std::chrono::milliseconds(10));
  }
  LOGI("stopping");
  client.stop();
  return 0;
}
