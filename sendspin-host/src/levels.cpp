// See levels.h.
#include "levels.h"

#include <algorithm>
#include <cmath>

namespace {
constexpr float kPi = 3.14159265358979f;
constexpr float kLowHz = 50.f, kHighHz = 16000.f;
constexpr int64_t kStaleUs = 200000;  // a frame this late is not worth drawing
constexpr size_t kMaxQueue = 64;      // ~2.5 s at 25 fps: more than any ALSA delay here

// In-place radix-2 FFT, n a power of two.
void fft(std::vector<float> &re, std::vector<float> &im) {
  const int n = int(re.size());
  for (int i = 1, j = 0; i < n; i++) {
    int bit = n >> 1;
    for (; j & bit; bit >>= 1) j ^= bit;
    j ^= bit;
    if (i < j) std::swap(re[i], re[j]), std::swap(im[i], im[j]);
  }
  for (int len = 2; len <= n; len <<= 1) {
    const float a = -2 * kPi / float(len);
    const float wr = std::cos(a), wi = std::sin(a);
    for (int i = 0; i < n; i += len) {
      float cr = 1, ci = 0;
      for (int k = 0; k < len / 2; k++) {
        const int p = i + k, q = p + len / 2;
        const float tr = re[q] * cr - im[q] * ci, ti = re[q] * ci + im[q] * cr;
        re[q] = re[p] - tr, im[q] = im[p] - ti;
        re[p] += tr, im[p] += ti;
        const float nr = cr * wr - ci * wi;
        ci = cr * wi + ci * wr, cr = nr;
      }
    }
  }
}
}  // namespace

void Levels::configure(uint32_t rate, uint8_t channels) {
  if (!enabled()) return;
  std::lock_guard<std::mutex> lk(mu_);
  rate_ = rate;
  channels_ = channels;
  hop_ = int(rate / kFps);
  since_ = 0;
  pos_ = 0;
  ring_.assign(kN, 0.f);
  re_.assign(kN, 0.f);
  im_.assign(kN, 0.f);
  if (win_.empty()) {
    win_.resize(kN);
    for (int i = 0; i < kN; i++) win_[i] = 0.5f - 0.5f * std::cos(2 * kPi * float(i) / float(kN - 1));
  }
  edges();
  q_.clear();
}

void Levels::reset() {
  if (!enabled()) return;
  std::lock_guard<std::mutex> lk(mu_);
  rate_ = 0;
  q_.clear();
}

// Log-spaced bands from kLowHz to kHighHz (capped at Nyquist), each at least one bin wide.
void Levels::edges() {
  lo_.assign(bands_, 0);
  hi_.assign(bands_, 0);
  const float bin = float(rate_) / kN;
  const float top = std::min(kHighHz, float(rate_) / 2);
  int prev = std::max(1, int(kLowHz / bin));
  for (int b = 0; b < bands_; b++) {
    const float f = kLowHz * std::pow(top / kLowHz, float(b + 1) / bands_);
    int h = std::min(kN / 2, std::max(prev + 1, int(std::lround(f / bin))));
    lo_[b] = prev;
    hi_[b] = h;
    prev = h;
  }
}

void Levels::feed(const int16_t *pcm, uint32_t frames, int64_t finish_us) {
  if (!enabled()) return;
  std::lock_guard<std::mutex> lk(mu_);
  if (!rate_ || !channels_) return;
  for (uint32_t i = 0; i < frames; i++) {
    float s = 0;
    for (int c = 0; c < channels_; c++) s += pcm[size_t(i) * channels_ + c];
    ring_[pos_] = s / (32768.f * channels_);
    pos_ = (pos_ + 1) % kN;
    if (++since_ >= hop_) {
      since_ = 0;
      // Sample i is heard at finish - (frames-1-i)/rate; the window is centred kN/2 before it.
      const int64_t at = finish_us - (int64_t(frames - 1 - i) + kN / 2) * 1000000 / rate_;
      analyse(at);
    }
  }
}

void Levels::analyse(int64_t at_us) {
  for (int i = 0; i < kN; i++) {
    re_[i] = ring_[(pos_ + i) % kN] * win_[i];
    im_[i] = 0;
  }
  fft(re_, im_);
  // A full-scale sine through the Hann window peaks at kN/4 in its bin.
  const float ref = float(kN) / 4;
  Frame fr{at_us, std::vector<uint8_t>(bands_)};
  for (int b = 0; b < bands_; b++) {
    float peak = 0;
    for (int k = lo_[b]; k < hi_[b]; k++) peak = std::max(peak, re_[k] * re_[k] + im_[k] * im_[k]);
    const float db = 10 * std::log10(peak / (ref * ref) + 1e-12f);
    fr.v[b] = uint8_t(std::lround(std::clamp((db - kFloorDb) / -kFloorDb, 0.f, 1.f) * 255));
  }
  if (q_.size() >= kMaxQueue) q_.pop_front();
  q_.push_back(std::move(fr));
}

bool Levels::due(int64_t now_us, std::vector<uint8_t> &out) {
  if (!enabled()) return false;
  std::lock_guard<std::mutex> lk(mu_);
  bool got = false;
  while (!q_.empty() && q_.front().at_us <= now_us) {
    if (now_us - q_.front().at_us <= kStaleUs) {
      out.swap(q_.front().v);
      got = true;
    }
    q_.pop_front();
  }
  return got;
}
