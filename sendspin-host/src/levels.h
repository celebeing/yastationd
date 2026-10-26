// Spectrum levels of the music being played, for the speaker's display (yastationd draws them):
// the PCM the sink writes, before the volume and the duck (the picture follows the music, not the
// knob), mixed to mono, a 2048-point FFT 25 times a second, log-spaced bands. Each frame is
// stamped with the time its audio is heard (the sink's finish time), and handed out when that
// time comes, so the bars move with the sound and not with the buffer ahead of it.
#pragma once

#include <cstdint>
#include <deque>
#include <mutex>
#include <vector>

class Levels {
 public:
  // bands 0 = off: feed() and due() do nothing.
  explicit Levels(int bands) : bands_(bands) {}

  // A stream started: its format (16-bit interleaved PCM).
  void configure(uint32_t rate, uint8_t channels);
  // The stream ended: drop the history and the frames not shown yet.
  void reset();
  // frames of PCM were written; the last one is heard at finish_us. Any thread.
  void feed(const int16_t *pcm, uint32_t frames, int64_t finish_us);
  // The newest frame whose time has come (older due ones are skipped): one byte per band,
  // 0 = -60 dB or quieter, 255 = full scale. Main loop.
  bool due(int64_t now_us, std::vector<uint8_t> &out);
  bool enabled() const { return bands_ > 0; }

 private:
  static constexpr int kN = 2048;   // FFT size
  static constexpr int kFps = 25;   // frames per second
  static constexpr float kFloorDb = -60.f;

  void analyse(int64_t at_us);
  void edges();

  struct Frame {
    int64_t at_us;
    std::vector<uint8_t> v;
  };

  const int bands_;
  std::mutex mu_;
  uint32_t rate_ = 0;
  uint8_t channels_ = 0;
  int hop_ = 0, since_ = 0;
  std::vector<float> ring_;  // the last kN mono samples
  int pos_ = 0;
  std::vector<float> win_, re_, im_;
  std::vector<int> lo_, hi_;  // FFT bin range per band [lo, hi)
  std::deque<Frame> q_;
};
