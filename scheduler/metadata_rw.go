package scheduler

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/Financial-Times/publish-carousel/s3"
)

const defaultContentType = "application/json"

type MetadataReadWriter interface {
	LoadMetadata(id string) (*CycleMetadata, error)
	WriteMetadata(id string, state CycleMetadata) error
}

type s3MetadataReadWriter struct {
	s3rw s3.ReadWriter
}

func NewS3MetadataReadWriter(rw s3.ReadWriter) MetadataReadWriter {
	return &s3MetadataReadWriter{s3rw: rw}
}

func (s *s3MetadataReadWriter) LoadMetadata(id string) (*CycleMetadata, error) {
	key, err := s.s3rw.GetLatestKeyForID(id)
	if err != nil {
		return nil, err
	}

	if strings.TrimSpace(key) == "" {
		return nil, errors.New(`No key found for id "` + id + `"`)
	}

	found, body, contentType, err := s.s3rw.Read(key)
	if err != nil {
		return nil, err
	}

	if !found {
		return nil, fmt.Errorf(`No state found for "%v"`, id)
	}

	if contentType == nil || strings.TrimSpace(*contentType) != "application/json" {
		return nil, fmt.Errorf(`Failed to load state for "%v". Content was in an unexpected Content-Type "%v"`, id, contentType)
	}

	state := &CycleMetadata{}
	dec := json.NewDecoder(body)
	err = dec.Decode(state)

	return state, err
}

func (s *s3MetadataReadWriter) WriteMetadata(id string, state CycleMetadata) error {
	b, err := json.Marshal(state)
	if err != nil {
		return err
	}

	key := time.Now().UTC().Format(`20060102T15040599`)
	return s.s3rw.Write(id, key, b, defaultContentType)
}