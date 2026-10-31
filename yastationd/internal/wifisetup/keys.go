package wifisetup

import (
	"bytes"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"net/http"
	"os"
	"strings"
)

// SSH keys from the setup page (docs/wifi-setup.md): an owner who came in through the access point
// has no other way to put a key on a speaker. They go to <cfw>/authorized_keys, which ssh.sh hands
// to OpenSSH next to the rootfs's file, so the read-only rootfs stays untouched. The page adds keys
// and lists them; removing one takes SSH.

type keysEdit struct {
	Key string `json:"key"`
}

// parseKey checks one authorized_keys line of a public key and returns it cleaned up: type, key,
// comment. Options in front of the type are refused: what the page accepts is a plain public key.
func parseKey(line string) (string, error) {
	f := strings.Fields(line)
	if len(f) < 2 {
		return "", errors.New("not an SSH public key: expected a line like \"ssh-ed25519 AAAA… name\"")
	}
	switch t := f[0]; {
	case t == "ssh-ed25519", t == "ssh-rsa", strings.HasPrefix(t, "ecdsa-sha2-"),
		strings.HasPrefix(t, "sk-ssh-ed25519@"), strings.HasPrefix(t, "sk-ecdsa-sha2-"):
	default:
		return "", errors.New("not an SSH public key: unknown key type " + t)
	}
	blob, err := base64.StdEncoding.DecodeString(f[1])
	if err != nil || len(blob) < 4 {
		return "", errors.New("not an SSH public key: the key part is not valid base64")
	}
	n := binary.BigEndian.Uint32(blob)
	if uint64(n) > uint64(len(blob)-4) || string(blob[4:4+n]) != f[0] {
		return "", errors.New("not an SSH public key: the key does not match its type")
	}
	return strings.Join(f, " "), nil
}

// keyLabel is how the page shows a key: its type and comment, or the end of the key without one.
func keyLabel(line string) string {
	f := strings.Fields(line)
	if len(f) < 2 {
		return line
	}
	if len(f) > 2 {
		return f[0] + " " + strings.Join(f[2:], " ")
	}
	return f[0] + " …" + f[1][max(0, len(f[1])-12):]
}

func (m *Mode) keyLabels() []string {
	labels := []string{}
	data, err := os.ReadFile(m.o.AuthorizedKeys)
	if err != nil {
		return labels
	}
	for _, l := range strings.Split(string(data), "\n") {
		if l = strings.TrimSpace(l); l != "" && !strings.HasPrefix(l, "#") {
			labels = append(labels, keyLabel(l))
		}
	}
	return labels
}

// POST /api/keys: add a public key; one already there stays as it is.
func (m *Mode) apiKeys(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		httpErr(w, http.StatusMethodNotAllowed, errors.New("POST only"))
		return
	}
	if m.o.AuthorizedKeys == "" {
		httpErr(w, http.StatusNotFound, errors.New("no file for SSH keys on this speaker"))
		return
	}
	var e keysEdit
	if err := readJSON(r, &e); err != nil {
		httpErr(w, http.StatusBadRequest, err)
		return
	}
	key, err := parseKey(e.Key)
	if err != nil {
		httpErr(w, http.StatusBadRequest, err)
		return
	}
	confMu.Lock()
	defer confMu.Unlock()
	old, err := os.ReadFile(m.o.AuthorizedKeys)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		httpErr(w, http.StatusInternalServerError, err)
		return
	}
	blob := strings.Fields(key)[1]
	for _, l := range strings.Split(string(old), "\n") {
		if f := strings.Fields(l); len(f) >= 2 && f[1] == blob {
			writeJSON(w, map[string]bool{"ok": true})
			return
		}
	}
	if len(old) > 0 && !bytes.HasSuffix(old, []byte("\n")) {
		old = append(old, '\n')
	}
	if err := WriteConfAtomic(m.o.AuthorizedKeys, append(old, key+"\n"...)); err != nil {
		httpErr(w, http.StatusInternalServerError, err)
		return
	}
	m.o.Log.Info("wifi setup: SSH key added", "key", keyLabel(key))
	writeJSON(w, map[string]bool{"ok": true})
}
