//go:build !darwin

package audio

import "errors"

const vpAvailable = false

type vpUnit struct{}

func startVP(*Engine) (*vpUnit, error) {
	return nil, errors.New("cancelamento de eco do sistema só existe no macOS")
}

func (*vpUnit) stop() {}

func MicrophoneMode() string { return "" }

func ShowMicrophoneModes() {}
