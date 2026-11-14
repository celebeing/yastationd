package app

import (
	"errors"
	"net/http"
	"slices"
	"strings"
	"time"
)

// Forget a speaker (docs/web-ui.md, "Forget a speaker"): any speaker clears what a gone one left
// on the broker — its availability, states, web and intercom cards, HA discovery configs and
// shared timers — by publishing empty retained messages over them. Every speaker drops it from its
// lists as the empty messages arrive, HA deletes its device. Nothing stops a speaker that comes
// back: it publishes everything again on connect and is listed again.

// forgetWait is how long the broker's replay of the retained messages is waited for.
const forgetWait = 1500 * time.Millisecond

var (
	errForgetSelf   = errors.New("a speaker cannot forget itself")
	errForgetOnline = errors.New("the broker still has it online: turn it off, or wait a minute if it just went")
	errForgetNone   = errors.New("the broker has nothing of this speaker")
	errNoBroker     = errors.New("not connected to the broker")
)

// retainedBus is the broker as forgetting needs it.
type retainedBus interface {
	Retained(filters []string, wait time.Duration) (map[string][]byte, error)
	Publish(topic, payload string) // retained
}

func (a *App) retainedBus() retainedBus {
	if a.forgetBus != nil {
		return a.forgetBus
	}
	if a.mqtt == nil || !a.mqtt.Connected() {
		return nil
	}
	return a.mqtt
}

// forgetSpeaker clears speaker id's retained messages; it reports how many.
func (a *App) forgetSpeaker(id string) (int, error) {
	if id == "" || strings.ContainsAny(id, "/+#") {
		return 0, errors.New("bad speaker id")
	}
	if id == a.cfg().DeviceID {
		return 0, errForgetSelf
	}
	bus := a.retainedBus()
	if bus == nil {
		return 0, errNoBroker
	}
	kept, err := bus.Retained(a.topics.DeviceRetained(id), forgetWait)
	if err != nil {
		return 0, err
	}
	if string(kept[a.topics.AvailabilityOf(id)]) == "online" {
		return 0, errForgetOnline
	}
	if len(kept) == 0 {
		a.dropPeer(id)
		return 0, errForgetNone
	}
	topics := make([]string, 0, len(kept))
	for t := range kept {
		topics = append(topics, t)
	}
	slices.Sort(topics)
	for _, t := range topics {
		bus.Publish(t, "")
	}
	a.dropPeer(id)
	a.log.Info("speaker forgotten", "id", id, "retained", len(topics))
	return len(topics), nil
}

// dropPeer takes speaker id off the app's list now (its empty web card does the same when it
// comes back from the broker).
func (a *App) dropPeer(id string) {
	a.web.mu.Lock()
	delete(a.web.peers, id)
	a.web.mu.Unlock()
}

// apiPeer: DELETE /api/peers/<id> forgets that speaker.
func (a *App) apiPeer(w http.ResponseWriter, r *http.Request) {
	id := strings.Trim(strings.TrimPrefix(r.URL.Path, "/api/peers/"), "/")
	if r.Method != http.MethodDelete {
		writeErr(w, http.StatusMethodNotAllowed, "DELETE")
		return
	}
	n, err := a.forgetSpeaker(id)
	switch {
	case errors.Is(err, errForgetNone):
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "cleared": 0})
	case errors.Is(err, errForgetOnline), errors.Is(err, errForgetSelf):
		writeErr(w, http.StatusConflict, err.Error())
	case errors.Is(err, errNoBroker):
		writeErr(w, http.StatusServiceUnavailable, err.Error())
	case err != nil:
		writeErr(w, http.StatusBadRequest, err.Error())
	default:
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "cleared": n})
	}
}
