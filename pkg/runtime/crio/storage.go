package crio

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// storageReader reads containers/storage metadata from well-known filesystem
// paths. This avoids importing the containers/storage library while still
// providing layer-level detail for CRI-O images.
type storageReader struct {
	root        string // e.g. /var/lib/containers/storage
	graphDriver string // e.g. overlay
}

func newStorageReader(root, graphDriver string) *storageReader {
	return &storageReader{root: root, graphDriver: graphDriver}
}

// ---------------------------------------------------------------------------
// Image records  —  <root>/<graphDriver>-images/images.json
// ---------------------------------------------------------------------------

type storageImageRecord struct {
	ID       string   `json:"id"`
	Digest   string   `json:"digest"`
	Names    []string `json:"names"`
	TopLayer string   `json:"layer"`
}

func (s *storageReader) readImageRecords() ([]storageImageRecord, error) {
	path := filepath.Join(s.root, s.graphDriver+"-images", "images.json")
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read images metadata %s: %w", path, err)
	}
	var records []storageImageRecord
	if err := json.Unmarshal(data, &records); err != nil {
		return nil, fmt.Errorf("parse images metadata: %w", err)
	}
	return records, nil
}

// ---------------------------------------------------------------------------
// Layer records  —  <root>/<graphDriver>-layers/layers.json
// ---------------------------------------------------------------------------

type storageLayerRecord struct {
	ID               string `json:"id"`
	Parent           string `json:"parent"`
	CompressedDigest string `json:"compressed-diff-digest"`
	CompressedSize   int64  `json:"compressed-size"`
	DiffDigest       string `json:"diff-digest"`
	DiffSize         int64  `json:"diff-size"`
	Compression      int    `json:"compression"`
}

func (s *storageReader) readLayerRecords() ([]storageLayerRecord, error) {
	path := filepath.Join(s.root, s.graphDriver+"-layers", "layers.json")
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read layers metadata %s: %w", path, err)
	}
	var records []storageLayerRecord
	if err := json.Unmarshal(data, &records); err != nil {
		return nil, fmt.Errorf("parse layers metadata: %w", err)
	}
	return records, nil
}

// ---------------------------------------------------------------------------
// Layer filesystem path  —  <root>/<graphDriver>/<layerID>/diff/
// ---------------------------------------------------------------------------

func (s *storageReader) layerDiffPath(layerID string) string {
	return filepath.Join(s.root, s.graphDriver, layerID, "diff")
}
