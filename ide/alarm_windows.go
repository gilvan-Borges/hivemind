//go:build windows

package main

import (
	"encoding/binary"
	"math"
	"sync"
	"syscall"
	"unsafe"
)

const (
	sndAsync     = 0x0001
	sndNoDefault = 0x0002
	sndMemory    = 0x0004
)

var (
	winmmDLL      = syscall.NewLazyDLL("winmm.dll")
	procPlaySound = winmmDLL.NewProc("PlaySoundW")

	alarmOnce      sync.Once
	alarmAttnWAV   []byte
	alarmErrWAV    []byte
	alarmPlayMutex sync.Mutex
	activeAlarmBuf []byte
)

func buildAlertWAV(isError bool) []byte {
	const (
		sampleRate = 22050
		durationMs = 2000
		numSamples = (sampleRate * durationMs) / 1000
	)

	pcm := make([]int16, numSamples)

	type pulse struct {
		startMs float64
		endMs   float64
		freqA   float64
		freqB   float64
	}
	var pulses []pulse
	if isError {
		// Alarme urgente de 2.0s: 4 pulsos graves/agudos em cadência rápida.
		pulses = []pulse{
			{0, 380, 960, 720},
			{480, 860, 960, 720},
			{960, 1340, 1020, 760},
			{1440, 1980, 1040, 720},
		}
	} else {
		// Chamado tático de 2.0s: 4 batidas duplas ascendentes bem audíveis.
		pulses = []pulse{
			{0, 220, 784, 1175},
			{250, 480, 1046.5, 1568},
			{650, 870, 784, 1175},
			{900, 1150, 1046.5, 1568},
			{1320, 1980, 1175, 1568},
		}
	}

	const twoPi = 2.0 * math.Pi
	for i := 0; i < numSamples; i++ {
		tMs := (float64(i) * 1000.0) / sampleRate
		tSec := float64(i) / sampleRate
		var sample float64
		for _, p := range pulses {
			if tMs < p.startMs || tMs > p.endMs {
				continue
			}
			localMs := tMs - p.startMs
			remMs := p.endMs - tMs
			env := 1.0
			if localMs < 14.0 {
				env = localMs / 14.0
			} else if remMs < 36.0 {
				env = remMs / 36.0
			}
			// Fundamental + 2º/3º harmônicos leves para cortar ruído de fundo sem distorcer.
			base := math.Sin(twoPi*p.freqA*tSec)*0.62 +
				math.Sin(twoPi*p.freqB*tSec)*0.30 +
				math.Sin(twoPi*p.freqA*2.0*tSec)*0.08
			sample += base * env * 0.42
		}
		if sample > 0.95 {
			sample = 0.95
		} else if sample < -0.95 {
			sample = -0.95
		}
		pcm[i] = int16(sample * 32767.0)
	}

	dataSize := len(pcm) * 2
	buf := make([]byte, 44+dataSize)
	copy(buf[0:4], "RIFF")
	binary.LittleEndian.PutUint32(buf[4:8], uint32(36+dataSize))
	copy(buf[8:12], "WAVE")
	copy(buf[12:16], "fmt ")
	binary.LittleEndian.PutUint32(buf[16:20], 16)
	binary.LittleEndian.PutUint16(buf[20:22], 1)
	binary.LittleEndian.PutUint16(buf[22:24], 1)
	binary.LittleEndian.PutUint32(buf[24:28], sampleRate)
	binary.LittleEndian.PutUint32(buf[28:32], sampleRate*2)
	binary.LittleEndian.PutUint16(buf[32:34], 2)
	binary.LittleEndian.PutUint16(buf[34:36], 16)
	copy(buf[36:40], "data")
	binary.LittleEndian.PutUint32(buf[40:44], uint32(dataSize))
	for i, s := range pcm {
		binary.LittleEndian.PutUint16(buf[44+i*2:46+i*2], uint16(s))
	}
	return buf
}

func playNativeAlertAlarm(mood string) bool {
	alarmOnce.Do(func() {
		alarmAttnWAV = buildAlertWAV(false)
		alarmErrWAV = buildAlertWAV(true)
	})
	wav := alarmAttnWAV
	if mood == "error" {
		wav = alarmErrWAV
	}
	if len(wav) == 0 {
		return false
	}

	alarmPlayMutex.Lock()
	activeAlarmBuf = wav
	ptr := uintptr(unsafe.Pointer(&activeAlarmBuf[0]))
	ret, _, _ := procPlaySound.Call(ptr, 0, uintptr(sndMemory|sndAsync|sndNoDefault))
	alarmPlayMutex.Unlock()
	return ret != 0
}
