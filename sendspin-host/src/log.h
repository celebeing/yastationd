// stderr logging (yastationd puts it in its own log); stdout is the event channel.
#pragma once

#include <cstdio>

#define LOGE(...) (std::fprintf(stderr, "sendspin-host: E " __VA_ARGS__), std::fputc('\n', stderr))
#define LOGW(...) (std::fprintf(stderr, "sendspin-host: W " __VA_ARGS__), std::fputc('\n', stderr))
#define LOGI(...) (std::fprintf(stderr, "sendspin-host: " __VA_ARGS__), std::fputc('\n', stderr))
