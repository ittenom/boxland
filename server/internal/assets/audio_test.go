package assets_test

import (
	"bytes"
	"encoding/binary"
	"testing"

	"boxland/server/internal/assets"
)

// makeWAV builds a minimal valid WAV blob with the given parameters and
// `dataSamples` zero-filled samples. The return is a byte slice ready to
// be sniffed by InspectAudio.
func makeWAV(t *testing.T, sampleRate, channels, bitDepth, dataSamples int) []byte {
	t.Helper()
	bytesPerSample := bitDepth / 8
	dataBytes := dataSamples * bytesPerSample * channels
	byteRate := sampleRate * channels * bytesPerSample
	blockAlign := channels * bytesPerSample

	var buf bytes.Buffer
	buf.WriteString("RIFF")
	_ = binary.Write(&buf, binary.LittleEndian, uint32(36+dataBytes))
	buf.WriteString("WAVE")

	buf.WriteString("fmt ")
	_ = binary.Write(&buf, binary.LittleEndian, uint32(16))
	_ = binary.Write(&buf, binary.LittleEndian, uint16(1)) // PCM
	_ = binary.Write(&buf, binary.LittleEndian, uint16(channels))
	_ = binary.Write(&buf, binary.LittleEndian, uint32(sampleRate))
	_ = binary.Write(&buf, binary.LittleEndian, uint32(byteRate))
	_ = binary.Write(&buf, binary.LittleEndian, uint16(blockAlign))
	_ = binary.Write(&buf, binary.LittleEndian, uint16(bitDepth))

	buf.WriteString("data")
	_ = binary.Write(&buf, binary.LittleEndian, uint32(dataBytes))
	buf.Write(make([]byte, dataBytes))
	return buf.Bytes()
}

func TestInspectAudio_WAVDuration(t *testing.T) {
	// 1 second of 44.1kHz mono 16-bit PCM = 44100 samples.
	wav := makeWAV(t, 44100, 1, 16, 44100)
	md, err := assets.InspectAudio(wav)
	if err != nil {
		t.Fatalf("InspectAudio: %v", err)
	}
	if md.Format != "wav" {
		t.Errorf("format: got %q", md.Format)
	}
	if md.SampleRate != 44100 || md.Channels != 1 || md.BitDepth != 16 {
		t.Errorf("header: got rate=%d ch=%d bd=%d", md.SampleRate, md.Channels, md.BitDepth)
	}
	if md.DurationMS < 990 || md.DurationMS > 1010 {
		t.Errorf("duration: got %dms, want ~1000ms", md.DurationMS)
	}
	if md.DefaultVolume != 200 {
		t.Errorf("default volume: got %d", md.DefaultVolume)
	}
}

func TestInspectAudio_WAVStereoHalfSecond(t *testing.T) {
	wav := makeWAV(t, 48000, 2, 24, 24000) // 0.5s
	md, err := assets.InspectAudio(wav)
	if err != nil {
		t.Fatal(err)
	}
	if md.DurationMS < 495 || md.DurationMS > 505 {
		t.Errorf("duration: got %dms, want ~500ms", md.DurationMS)
	}
	if md.Channels != 2 || md.BitDepth != 24 {
		t.Errorf("stereo/24-bit not preserved: %+v", md)
	}
}

func TestInspectAudio_OGGRecognizedDurationUnknown(t *testing.T) {
	// Minimal OGG marker; we don't decode, just detect.
	body := []byte("OggSnotreallyanoggfile")
	md, err := assets.InspectAudio(body)
	if err != nil {
		t.Fatal(err)
	}
	if md.Format != "ogg" {
		t.Errorf("format: got %q", md.Format)
	}
	if md.DurationMS != 0 {
		t.Errorf("OGG duration should be 0 (unknown) in v1, got %d", md.DurationMS)
	}
}

func TestInspectAudio_MP3Recognized(t *testing.T) {
	// ID3 header
	body := append([]byte("ID3"), make([]byte, 50)...)
	md, err := assets.InspectAudio(body)
	if err != nil {
		t.Fatal(err)
	}
	if md.Format != "mp3" {
		t.Errorf("format: got %q", md.Format)
	}
}

func TestInspectAudio_MP3SyncByte(t *testing.T) {
	// MPEG sync (no ID3)
	body := append([]byte{0xff, 0xfb, 0x00, 0x00}, make([]byte, 50)...)
	md, err := assets.InspectAudio(body)
	if err != nil {
		t.Fatal(err)
	}
	if md.Format != "mp3" {
		t.Errorf("format: got %q", md.Format)
	}
}

func TestInspectAudio_UnknownFormat(t *testing.T) {
	if _, err := assets.InspectAudio([]byte("plain text")); err == nil {
		t.Error("expected error for unrecognized format")
	}
}

func TestInspectAudio_TruncatedWAVRejected(t *testing.T) {
	wav := makeWAV(t, 44100, 1, 16, 100)[:30] // chop mid-header
	if _, err := assets.InspectAudio(wav); err == nil {
		t.Error("expected error for truncated WAV")
	}
}
