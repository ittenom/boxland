package assets

import (
	"bytes"
	"context"
	"strings"
)

// FreeTexPackerImporter handles the free-tex-packer JSON export.
//
// free-tex-packer (https://free-tex-packer.com) emits the same per-frame
// shape TexturePacker does. The only practical difference is the meta.app
// marker; we tell them apart so the auto-detect attribution is correct
// in logs and the UI dropdown.
type FreeTexPackerImporter struct{}

func (*FreeTexPackerImporter) ID() string { return "free-tex-packer" }

func (*FreeTexPackerImporter) CanAutoDetect(filename string, body []byte) bool {
	if !strings.HasSuffix(strings.ToLower(filename), ".json") {
		return false
	}
	return bytes.Contains(body, []byte(`free-tex-packer.com`))
}

// Parse delegates to TexturePackerImporter and patches the metadata source
// label so the row in assets.metadata_json identifies the true tool.
func (p *FreeTexPackerImporter) Parse(ctx context.Context, body []byte, configJSON []byte) (*ImportResult, error) {
	tp := &TexturePackerImporter{}
	out, err := tp.Parse(ctx, body, configJSON)
	if err != nil {
		return nil, err
	}
	out.SheetMetadata.Source = "free-tex-packer"
	return out, nil
}
