// SPDX-License-Identifier: MPL-2.0
// Adapted from XTLS/Xray-core 0afec66085a85191e9dd2a4a6646c7c9a94688d1.
package xdrive

import (
	"context"

	"github.com/xtls/xray-core/common/errors"
)

var errNotFound = errors.New("object not found")

type Entry struct {
	Name   string
	Inline []byte
}

type Storage interface {
	Put(ctx context.Context, name string, data []byte) error
	Get(ctx context.Context, name string) ([]byte, error)
	Delete(ctx context.Context, name string) error
	List(ctx context.Context, prefix string) ([]Entry, error)
	Close() error
}

func newStorage(streamSettings *streamOptions) (Storage, error) {
	streamSettings.storageOnce.Do(func() { streamSettings.storage, streamSettings.storageErr = buildStorage(streamSettings) })
	return streamSettings.storage, streamSettings.storageErr
}

func buildStorage(streamSettings *streamOptions) (Storage, error) {
	config, err := streamConfig(streamSettings)
	if err != nil {
		return nil, err
	}

	switch config.Service {
	case "local":
		return newLocalStorage(config.RemoteFolder)
	case "google-drive", "Google Drive":
		return sharedStorage(streamSettings, config, func() (Storage, error) {
			return newDriveStorage(streamSettings, config)
		})
	case "template":
		return sharedStorage(streamSettings, config, func() (Storage, error) {
			return newTemplateStorage(streamSettings, config)
		})
	case "s3":
		return sharedStorage(streamSettings, config, func() (Storage, error) {
			return newS3Storage(streamSettings, config)
		})
	default:
		return nil, errors.New("unsupported service: ", config.Service)
	}
}

// sessionStorage returns the storage of one session, which takes connections
// of XMUX until Close, with the token and cache of storage. Local storage has
// no connections
func sessionStorage(storage Storage) Storage {
	switch s := storage.(type) {
	case *driveStorage:
		return s.session()
	case *templateStorage:
		return s.session()
	case *s3Storage:
		return s.session()
	}
	return storage
}

// Sharing is scoped to one native transport; different detours must never reuse
// a storage HTTP client constructed by another outbound.
func sharedStorage(streamSettings *streamOptions, config *Config, build func() (Storage, error)) (Storage, error) {
	return build()
}
