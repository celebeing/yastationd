// Adapted from pulsar's satellite/src/sendspin/sendspin_service.cpp AlsaSink
// (MIT, (c) 2026 pulsar contributors). See alsa_sink.h.
#include "alsa_sink.h"

#include <algorithm>
#include <cmath>
#include <cstdio>

#include "log.h"
#include "sendspin/client.h"

namespace {
constexpr int kWaitSliceMs = 20;         // longest snd_pcm_wait() slice inside one write
constexpr float kDuckRampSec = 0.3f;     // full-scale gain ramp (ducking in and out)
constexpr int64_t kStatsPeriodUs = 30000000;
constexpr int64_t kReopenRetryUs = 1000000;

int64_t now_us() { return sendspin::SendspinClient::now_us(); }
}  // namespace

AlsaSink::~AlsaSink() {
  std::lock_guard<std::mutex> lk(io_mu_);
  close_locked();
}

void AlsaSink::configure(uint32_t rate, uint8_t channels, uint8_t bits) {
  request(Req{Req::CONFIGURE, rate, channels, bits});
}

void AlsaSink::release() { request(Req{Req::RELEASE, 0, 0, 0}); }

void AlsaSink::set_volume(uint8_t volume, bool muted) {
  float v = std::min<uint8_t>(volume, 100) / 100.f;
  volume_gain_.store(muted ? 0.f : v * v);
}

void AlsaSink::set_duck(float level) { duck_.store(std::clamp(level, 0.f, 1.f)); }

size_t AlsaSink::write(const uint8_t *data, size_t length, uint32_t timeout_ms) {
  size_t r = write_once(data, length, timeout_ms);
  // A request that arrived while io_mu_ was held (request()'s try_lock failed) must not wait for
  // the next write: a RELEASE may be the last event, and the device would stay open.
  if (req_gen_.load() != applied_gen_.load()) {
    std::lock_guard<std::mutex> lk(io_mu_);
    apply_pending_locked();
  }
  return r;
}

size_t AlsaSink::write_once(const uint8_t *data, size_t length, uint32_t timeout_ms) {
  const int64_t deadline = now_us() + int64_t(timeout_ms) * 1000;
  uint32_t frames_done = 0;
  int64_t finish_us = 0;
  size_t bpf = 0;
  bool lost = false;
  uint32_t report = 0;
  {
    std::lock_guard<std::mutex> lk(io_mu_);
    apply_pending_locked();
    bpf = size_t(want_channels_) * (want_bits_ / 8);
    if (!active_ || bpf == 0) return length;  // stale audio after stream end: discard
    if (!pcm_ && !open_locked()) {
      // Device unavailable: discard rather than spin the task; the gap is reported as played
      // with the next timed report, so the library's buffered-frames estimate stays right.
      discarded_ += uint32_t(length / bpf);
      return length - length % bpf;
    }
    const size_t frames_total = length / bpf;
    const uint8_t *src = scale_locked(data, frames_total);

    bool first = true;
    while (frames_done < frames_total && pcm_) {
      if (!first && now_us() >= deadline) break;
      first = false;
      snd_pcm_sframes_t avail = snd_pcm_avail_update(pcm_);
      if (avail < 0) {
        if (!recover_locked(int(avail))) break;
        continue;
      }
      if (avail == 0) {
        int64_t left_ms = (deadline - now_us()) / 1000;
        if (left_ms <= 0) break;
        int r = snd_pcm_wait(pcm_, int(std::min<int64_t>(left_ms, kWaitSliceMs)));
        if (r < 0 && !recover_locked(r)) break;
        if (req_gen_.load() != applied_gen_.load()) {  // stream restarted/ended: this audio is stale
          apply_pending_locked();
          return length;
        }
        continue;
      }
      snd_pcm_uframes_t chunk =
          std::min<snd_pcm_uframes_t>(frames_total - frames_done, snd_pcm_uframes_t(avail));
      snd_pcm_sframes_t w = snd_pcm_writei(pcm_, src + size_t(frames_done) * bpf, chunk);
      if (w < 0) {
        if (!recover_locked(int(w))) break;
        continue;
      }
      frames_done += uint32_t(w);
    }
    // Commit the ramp by the frames actually written; an unwritten tail is presented again.
    gain_ = ramp_end_gain(frames_done);

    if (frames_done > 0 && pcm_) {
      // The frames just written finish playing once everything queued ahead of the DAC (our ring,
      // the plug/rate converter, the dmix ring) has played: now + snd_pcm_delay().
      snd_pcm_sframes_t delay = 0;
      if (snd_pcm_delay(pcm_, &delay) == 0 && delay >= 0) {
        const int64_t now = now_us();
        finish_us = now + int64_t(delay) * 1000000 / rate_;
        last_delay_frames_ = delay;
        delay_min_ = std::min(delay_min_, delay);
        delay_max_ = std::max(delay_max_, delay);
        if (!first_write_us_) first_write_us_ = now;
      }
    }
    frames_total_ += frames_done;
    maybe_log_stats_locked();
    lost = !pcm_;
    if (lost && frames_done == 0) discarded_ += uint32_t(length / bpf);
    if (finish_us) {
      report = frames_done + discarded_;
      discarded_ = 0;
    }
  }
  if (report && on_played) on_played(report, finish_us);
  if (frames_done && finish_us && on_written) on_written(data, frames_done, finish_us);
  if (lost && frames_done == 0) return length - length % bpf;  // device lost mid-write: discard
  return size_t(frames_done) * bpf;
}

void AlsaSink::request(Req r) {
  {
    std::lock_guard<std::mutex> lk(req_mu_);
    req_ = r;
    req_gen_.fetch_add(1);
  }
  std::unique_lock<std::mutex> lk(io_mu_, std::try_to_lock);
  if (lk) apply_pending_locked();
}

void AlsaSink::apply_pending_locked() {
  Req r;
  {
    std::lock_guard<std::mutex> lk(req_mu_);
    applied_gen_.store(req_gen_.load());
    r = req_;
    req_.kind = Req::NONE;
  }
  if (r.kind == Req::CONFIGURE) apply_configure_locked(r.rate, r.channels, r.bits);
  else if (r.kind == Req::RELEASE) apply_release_locked();
}

void AlsaSink::apply_configure_locked(uint32_t rate, uint8_t channels, uint8_t bits) {
  if (pcm_ && (rate != rate_ || channels != channels_ || bits != bits_)) close_locked();
  if (pcm_) {  // same format: restart from an empty ring
    snd_pcm_drop(pcm_);
    if (snd_pcm_prepare(pcm_) < 0) close_locked();
  }
  want_rate_ = rate;
  want_channels_ = channels;
  want_bits_ = bits;
  active_ = true;
  open_failed_logged_ = false;
  next_open_us_ = 0;
  discarded_ = 0;
  gain_ = target();  // a new stream starts at the current level, no ramp
}

void AlsaSink::apply_release_locked() {
  active_ = false;
  if (pcm_) {
    LOGI("alsa: stream done: %.1f s written, xruns %u", rate_ ? double(frames_total_) / rate_ : 0.0, xruns_);
    snd_pcm_drop(pcm_);
  }
  close_locked();
}

bool AlsaSink::open_locked() {
  const int64_t now = now_us();
  if (now < next_open_us_) return false;
  if (want_bits_ != 16) {  // only 16-bit formats are advertised
    LOGE("alsa: unsupported bit depth %u", want_bits_);
    next_open_us_ = now + kReopenRetryUs;
    return false;
  }
  int err = snd_pcm_open(&pcm_, pcm.c_str(), SND_PCM_STREAM_PLAYBACK, 0);
  const char *step = "open";
  snd_pcm_hw_params_t *hw = nullptr;
  snd_pcm_hw_params_alloca(&hw);
  snd_pcm_uframes_t period = 0, buffer = 0;
  if (err >= 0) step = "hw_params_any", err = snd_pcm_hw_params_any(pcm_, hw);
  if (err >= 0) step = "access", err = snd_pcm_hw_params_set_access(pcm_, hw, SND_PCM_ACCESS_RW_INTERLEAVED);
  if (err >= 0) step = "format", err = snd_pcm_hw_params_set_format(pcm_, hw, SND_PCM_FORMAT_S16_LE);
  if (err >= 0) step = "channels", err = snd_pcm_hw_params_set_channels(pcm_, hw, want_channels_);
  if (err >= 0) step = "rate", err = snd_pcm_hw_params_set_rate(pcm_, hw, want_rate_, 0);
  if (err >= 0) step = "hw_params", err = snd_pcm_hw_params(pcm_, hw);
  if (err >= 0) {
    snd_pcm_hw_params_get_period_size(hw, &period, nullptr);
    snd_pcm_hw_params_get_buffer_size(hw, &buffer);
    snd_pcm_sw_params_t *sw = nullptr;
    snd_pcm_sw_params_alloca(&sw);
    step = "sw_params";
    err = snd_pcm_sw_params_current(pcm_, sw);
    // Start as soon as one period is queued, so snd_pcm_delay() tracks real playback early.
    if (err >= 0) err = snd_pcm_sw_params_set_start_threshold(pcm_, sw, period ? period : 1);
    if (err >= 0) err = snd_pcm_sw_params_set_avail_min(pcm_, sw, period ? period : 1);
    if (err >= 0) err = snd_pcm_sw_params(pcm_, sw);
  }
  if (err >= 0) step = "prepare", err = snd_pcm_prepare(pcm_);
  if (err < 0) {
    if (!open_failed_logged_)
      LOGE("alsa: '%s' %s failed for %u Hz/%u ch: %s", pcm.c_str(), step, want_rate_, want_channels_,
           snd_strerror(err));
    open_failed_logged_ = true;
    close_locked();
    next_open_us_ = now + kReopenRetryUs;
    return false;
  }
  rate_ = want_rate_;
  channels_ = want_channels_;
  bits_ = want_bits_;
  xruns_ = 0;
  frames_total_ = 0;
  first_write_us_ = 0;
  delay_min_ = INT32_MAX;
  delay_max_ = 0;
  stats_us_ = now;
  LOGI("alsa: '%s' open %u Hz/%u ch (period %lu, buffer %lu frames = %.0f ms)", pcm.c_str(), rate_, channels_,
       (unsigned long)period, (unsigned long)buffer, buffer * 1000.0 / rate_);
  return true;
}

void AlsaSink::close_locked() {
  if (!pcm_) return;
  snd_pcm_close(pcm_);
  pcm_ = nullptr;
}

bool AlsaSink::recover_locked(int err) {
  if (err == -EINTR || err == -EAGAIN) return true;
  if (err == -EPIPE || err == -ESTRPIPE) {
    xruns_++;
    int r = err == -ESTRPIPE ? snd_pcm_resume(pcm_) : -1;
    if (r < 0) r = snd_pcm_prepare(pcm_);
    if (r >= 0) return true;
    err = r;
  }
  LOGE("alsa: error %s, closing the device", snd_strerror(err));
  close_locked();
  next_open_us_ = now_us() + kReopenRetryUs;
  return false;
}

// Gain after `frames` frames of the linear ramp from gain_ toward the target.
float AlsaSink::ramp_end_gain(uint32_t frames) const {
  const float t = target();
  const float d = float(frames) / (kDuckRampSec * float(rate_ ? rate_ : 48000));
  return gain_ < t ? std::min(t, gain_ + d) : std::max(t, gain_ - d);
}

const uint8_t *AlsaSink::scale_locked(const uint8_t *data, size_t frames) {
  const float t = target();
  if (gain_ == t && t >= 1.f) return data;
  scratch_.resize(frames * channels_);
  const int16_t *in = reinterpret_cast<const int16_t *>(data);
  const float step = 1.f / (kDuckRampSec * float(rate_));
  float g = gain_;
  for (size_t f = 0; f < frames; f++) {
    if (g < t) g = std::min(t, g + step);
    else if (g > t) g = std::max(t, g - step);
    for (unsigned c = 0; c < channels_; c++) {
      size_t i = f * channels_ + c;
      scratch_[i] = int16_t(std::lrintf(float(in[i]) * g));
    }
  }
  return reinterpret_cast<const uint8_t *>(scratch_.data());
}

void AlsaSink::maybe_log_stats_locked() {
  const int64_t now = now_us();
  if (!rate_ || !first_write_us_ || now - stats_us_ < kStatsPeriodUs) return;
  stats_us_ = now;
  LOGI("alsa: %.1f s played, output delay %.1f ms (min %.1f, max %.1f), xruns %u, gain %.2f",
       double(int64_t(frames_total_) - last_delay_frames_) / rate_, last_delay_frames_ * 1000.0 / rate_,
       delay_min_ * 1000.0 / rate_, delay_max_ * 1000.0 / rate_, xruns_, gain_);
  delay_min_ = INT32_MAX;
  delay_max_ = 0;
}
