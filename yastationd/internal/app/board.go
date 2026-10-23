package app

import (
	"github.com/celebeing/yastationd/yastationd/internal/board"
	"github.com/celebeing/yastationd/yastationd/internal/config"
)

// resolveBoard loads the board profile (built-in, detected or named, plus the override file)
// and applies the hardware keys that are explicitly set in the daemon config on top.
func resolveBoard(cfg config.Config) (board.Profile, error) {
	p, err := board.Load(cfg.SysRoot, cfg.Board, cfg.BoardFile)
	if err != nil {
		return p, err
	}
	if cfg.Has("aplay_device") {
		p.Audio.Playback = cfg.AplayDevice
	}
	if cfg.Has("amixer_card") {
		p.Audio.Card = cfg.AmixerCard
	}
	if cfg.Has("unmute") {
		p.Audio.Unmute = cfg.Unmute
	}
	if cfg.Has("mic_pdm_gain") {
		p.Audio.MicGainValue = cfg.MicPDMGain
	}
	if cfg.Has("mic_device") {
		p.Mic.Device = cfg.MicDevice
	}
	if cfg.Has("mic_channels") {
		p.Mic.Channels = cfg.MicChannels
	}
	if cfg.Has("mic_rate") {
		p.Mic.Rate = cfg.MicRate
	}
	if cfg.Has("mic_channel") {
		p.Mic.Voice = cfg.MicChannel
	}
	if cfg.Has("wake_model") {
		p.Wake.Model = cfg.WakeModel
	}
	if cfg.Has("command_model") {
		p.Wake.CommandModel = cfg.CommandModel
	}
	if cfg.Has("spotter_libs") {
		p.Wake.Libs = cfg.SpotterLibs
	}
	return p, p.Validate()
}
