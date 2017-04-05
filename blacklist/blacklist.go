package blacklist

import (
	"bufio"
	"bytes"
	"errors"
	"os"

	"github.com/Financial-Times/publish-carousel/native"
)

// Blacklist interface type for external use
type Blacklist interface {
	ValidForPublish(uuid string, content *native.Content) (bool, error)
}

type chainedBlacklist struct {
	filter blacklistFilter
}

type blacklistFilter func(uuid string, content *native.Content) (bool, error)

// Builder builds a new blacklist with the required filters
type Builder struct {
	chain []blacklistFilter
	errs  []error
}

// NewBuilder Create a new blacklist builder
func NewBuilder() *Builder {
	return &Builder{}
}

func (c *chainedBlacklist) ValidForPublish(uuid string, content *native.Content) (bool, error) {
	return c.filter(uuid, content)
}

// FileBasedBlacklist provides a file based blacklist, which checks if the given uuid is contained within the given file.
func (b *Builder) FileBasedBlacklist(file string) *Builder {
	filter, err := fileBasedBlacklist(file)
	if err != nil {
		b.errs = append(b.errs, err)
		return b
	}

	b.chain = append(b.chain, filter)
	return b
}

// Build returns the Blacklist instance for use
func (b *Builder) Build() (Blacklist, error) {
	if len(b.errs) > 0 {
		msg := ""
		for _, err := range b.errs {
			msg = `"` + err.Error() + `", `
		}
		return nil, errors.New(msg)
	}

	return &chainedBlacklist{
		filter: func(uuid string, content *native.Content) (bool, error) {
			for _, filter := range b.chain {
				valid, err := filter(uuid, content)
				if !valid || err != nil {
					return valid, err
				}
			}
			return true, nil
		},
	}, nil
}

func fileBasedBlacklist(file string) (blacklistFilter, error) {
	f, err := os.Open(file)
	if f != nil {
		defer f.Close()
	}

	if err != nil {
		return nil, err
	}

	return func(uuid string, content *native.Content) (bool, error) {
		f, err := os.Open(file)
		if err != nil {
			return false, err
		}

		defer f.Close()
		scanner := bufio.NewScanner(f)
		for scanner.Scan() {
			if bytes.Contains(scanner.Bytes(), []byte(uuid)) {
				return false, nil
			}
		}

		err = scanner.Err()
		return true, err
	}, nil
}