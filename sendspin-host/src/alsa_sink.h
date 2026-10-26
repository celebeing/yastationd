// ALSA output for the Sendspin player: decoded PCM to a dmix PCM (the device's `default`), shared
// with yastationd's own aplay streams, with the playback-time feedback Sendspin's sync needs, and
// a software gain = server volume x duck level, ramped so ducking never clicks.
//
// Adapted from pulsar's satellite/src/sendspin/sendspin_service.cpp AlsaSink
// (MIT, (c) 2026 pulsar contributors).
#pragma once

#include <alsa/asoundlib.h>

#include <atomic>
#include <cstdint>
#include <functional>
#include <mutex>
#include <string>
#include <vector>

// configure()/release() run on the main loop thread (stream start/end), write() on the library's
// sync task. The PCM is opened lazily by write(), so the loop never blocks on it. io_mu_ is held
// by write() for its whole duration (including snd_pcm_wait slices), so the loop thread never
// takes it blocking: configure()/release() record a request and apply it at once if the audio
// thread is idle (try_lock); otherwise the audio thread applies it at its next wait slice and
// abandons the write in progress.
class AlsaSink {
 public:
  std::function<void(uint32_t frames, int64_t finish_us)> on_played;
  // The PCM (as received, before the gain) of frames that reached the device; the last one is
  // heard at finish_us. Called on the sync task, outside the sink's locks.
  std::function<void(const uint8_t *data, uint32_t frames, int64_t finish_us)> on_written;
  std::string pcm = "default";

  ~AlsaSink();

  void configure(uint32_t rate, uint8_t channels, uint8_t bits);
  // Stream ended: drop what is queued and release the device, so dmix is not held while idle.
  void release();
  // Server volume 0..100 (quadratic curve, like the sendspin-cpp examples) and mute.
  void set_volume(uint8_t volume, bool muted);
  // Duck level 0..1 (1 = no ducking), ramped over kDuckRampSec.
  void set_duck(float level);

  size_t write(const uint8_t *data, size_t length, uint32_t timeout_ms);

 private:
  struct Req {
    enum Kind { NONE, CONFIGURE, RELEASE } kind;
    uint32_t rate;
    uint8_t channels, bits;
  };

  size_t write_once(const uint8_t *data, size_t length, uint32_t timeout_ms);
  void request(Req r);
  void apply_pending_locked();
  void apply_configure_locked(uint32_t rate, uint8_t channels, uint8_t bits);
  void apply_release_locked();
  bool open_locked();
  void close_locked();
  bool recover_locked(int err);
  float target() const { return volume_gain_.load() * duck_.load(); }
  float ramp_end_gain(uint32_t frames) const;
  const uint8_t *scale_locked(const uint8_t *data, size_t frames);
  void maybe_log_stats_locked();

  std::mutex io_mu_;
  std::mutex req_mu_;
  Req req_{Req::NONE, 0, 0, 0};
  std::atomic<uint32_t> req_gen_{0};
  std::atomic<uint32_t> applied_gen_{0};
  snd_pcm_t *pcm_ = nullptr;
  bool active_ = false;
  uint32_t want_rate_ = 0, rate_ = 0;
  uint8_t want_channels_ = 0, channels_ = 0, want_bits_ = 0, bits_ = 0;
  bool open_failed_logged_ = false;
  int64_t next_open_us_ = 0;
  uint32_t discarded_ = 0;  // frames consumed without reaching the device, not yet reported
  std::atomic<float> volume_gain_{1.f};
  std::atomic<float> duck_{1.f};
  float gain_ = 1.f;  // current (ramping) gain; sync task only, under io_mu_
  std::vector<int16_t> scratch_;
  // stats
  uint64_t frames_total_ = 0;
  unsigned xruns_ = 0;
  snd_pcm_sframes_t last_delay_frames_ = 0, delay_min_ = INT32_MAX, delay_max_ = 0;
  int64_t first_write_us_ = 0, stats_us_ = 0;
};
