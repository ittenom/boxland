// Boxland — audio asset metadata.
//
// Reads format-specific headers to extract duration and basic properties.
// Stored as the assets.metadata_json payload for kind = "audio".
//
// v1 support:
//   * WAV  -- full duration extraction via RIFF header parsing (pure Go)
//   * OGG  -- detected via magic bytes; duration = 0 (unknown) for v1
//   * MP3  -- detected via magic bytes; duration = 0 (unknown) for v1
//
// OGG and MP3 duration extraction is deferred to keep dep weight low; the
// audio detail panel (task #62) renders "duration unknown" placeholders
// for those cases until proper parsers are wired in.

package assets

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
)

// AudioMetadata is the structured payload stored in assets.metadata_json
// for kind = "audio".
type AudioMetadata struct {
	Format        string `json:"format"`        // "wav" | "ogg" | "mp3"
	DurationMS    int    `json:"duration_ms"`   // 0 = unknown (e.g., v1 OGG/MP3)
	SampleRate    int    `json:"sample_rate"`   // 0 if unknown
	Channels      int    `json:"channels"`      // 0 if unknown
	BitDepth      int    `json:"bit_depth"`     // 0 if not applicable (compressed)
	Loopable      bool   `json:"loopable"`      // designer toggle, default false
	DefaultVolume int    `json:"default_volume"` // 0..255, default 200
}

// DefaultAudioMetadata returns the metadata used when the format is
// recognized but no header detail is extracted. Designers can edit
// loopable + default_volume in the UI.
func DefaultAudioMetadata(format string) AudioMetadata {
	return AudioMetadata{
		Format:        format,
		Loopable:      false,
		DefaultVolume: 200,
	}
}

// InspectAudio returns metadata for the given audio bytes. Detects format
// via magic bytes and dispatches to a per-format parser. Returns an error
// only if the bytes are clearly not audio of any supported format.
func InspectAudio(body []byte) (*AudioMetadata, error) {
	switch {
	case isWAV(body):
		return inspectWAV(body)
	case isOGG(body):
		md := DefaultAudioMetadata("ogg")
		return &md, nil
	case isMP3(body):
		md := DefaultAudioMetadata("mp3")
		return &md, nil
	default:
		return nil, errors.New("audio: format not recognized")
	}
}

// ---- WAV ----

func isWAV(b []byte) bool {
	return len(b) >= 12 &&
		bytes.Equal(b[:4], []byte("RIFF")) &&
		bytes.Equal(b[8:12], []byte("WAVE"))
}

// inspectWAV parses the canonical RIFF / WAVE header. Standard layout:
//   off 0..4    "RIFF"
//   off 4..8    file size - 8 (uint32 LE)
//   off 8..12   "WAVE"
//   off 12..16  "fmt "
//   off 16..20  fmt chunk size (16 for PCM)
//   off 20..22  audio format (1 = PCM)
//   off 22..24  num channels
//   off 24..28  sample rate
//   off 28..32  byte rate
//   off 32..34  block align
//   off 34..36  bits per sample
//
// Then a "data" chunk with size; duration = data_size / byte_rate.
//
// Real-world WAVs sometimes interpose other chunks before "data"; we walk
// chunks until we find "data" rather than assuming the canonical offset.
func inspectWAV(b []byte) (*AudioMetadata, error) {
	if len(b) < 44 {
		return nil, errors.New("wav: file too small to contain a header")
	}
	md := DefaultAudioMetadata("wav")

	// Walk chunks starting at offset 12.
	pos := 12
	for pos+8 <= len(b) {
		id := string(b[pos : pos+4])
		size := binary.LittleEndian.Uint32(b[pos+4 : pos+8])
		body := b[pos+8:]
		if uint32(len(body)) < size {
			return nil, fmt.Errorf("wav: chunk %q truncated", id)
		}
		switch id {
		case "fmt ":
			if size < 16 {
				return nil, errors.New("wav: fmt chunk too small")
			}
			md.Channels = int(binary.LittleEndian.Uint16(body[2:4]))
			md.SampleRate = int(binary.LittleEndian.Uint32(body[4:8]))
			md.BitDepth = int(binary.LittleEndian.Uint16(body[14:16]))
		case "data":
			if md.SampleRate <= 0 || md.Channels <= 0 || md.BitDepth <= 0 {
				return nil, errors.New("wav: data chunk before fmt chunk")
			}
			byteRate := md.SampleRate * md.Channels * md.BitDepth / 8
			if byteRate > 0 {
				md.DurationMS = int(int64(size) * 1000 / int64(byteRate))
			}
			return &md, nil
		}
		// Chunks are word-aligned; skip a pad byte if size is odd.
		pos += 8 + int(size)
		if size%2 == 1 {
			pos++
		}
	}
	return nil, errors.New("wav: no data chunk found")
}

// ---- OGG ----

func isOGG(b []byte) bool {
	return len(b) >= 4 && bytes.Equal(b[:4], []byte("OggS"))
}

// ---- MP3 ----

// isMP3 returns true for files starting with an ID3 tag or an MPEG sync
// frame (FFE/FFF). Both are the standard "this is an MP3" markers.
func isMP3(b []byte) bool {
	if len(b) >= 3 && bytes.Equal(b[:3], []byte("ID3")) {
		return true
	}
	if len(b) >= 2 && b[0] == 0xff && (b[1]&0xe0) == 0xe0 {
		return true
	}
	return false
}
