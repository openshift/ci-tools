package dispatcher

import (
	"encoding/gob"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// GobWriteCommittedError reports a failure that happened after the destination
// file was atomically replaced. Callers must publish the new in-memory state,
// but should retry so the directory entry can be durably synced.
type GobWriteCommittedError struct {
	// Err is the underlying error encountered after the Gob file was replaced.
	Err error
}

// Error returns the underlying committed-write error message.
func (e *GobWriteCommittedError) Error() string {
	return e.Err.Error()
}

// Unwrap returns the underlying committed-write error for errors.Is and errors.As.
func (e *GobWriteCommittedError) Unwrap() error {
	return e.Err
}

// IsGobWriteCommitted reports whether err indicates that the Gob destination was replaced before a later failure.
func IsGobWriteCommitted(err error) bool {
	var committedErr *GobWriteCommittedError
	return errors.As(err, &committedErr)
}

func ReadGob(filename string, data interface{}) error {
	file, err := os.Open(filename)
	if err != nil {
		return err
	}
	defer file.Close()

	decoder := gob.NewDecoder(file)
	err = decoder.Decode(data)
	if err != nil {
		return err
	}

	return nil
}

func WriteGob(filename string, data interface{}) error {
	return writeGob(filename, data, syncGobDirectory)
}

func writeGob(filename string, data interface{}, syncDirectory func(string) error) error {
	directory := filepath.Dir(filename)
	temporary, err := os.CreateTemp(directory, "."+filepath.Base(filename)+".tmp-")
	if err != nil {
		return fmt.Errorf("failed to create temporary Gob file: %w", err)
	}
	temporaryName := temporary.Name()
	closed := false
	defer func() {
		if !closed {
			_ = temporary.Close()
		}
		_ = os.Remove(temporaryName)
	}()

	if err := temporary.Chmod(0o644); err != nil {
		return fmt.Errorf("failed to set permissions on temporary Gob file: %w", err)
	}
	if err := gob.NewEncoder(temporary).Encode(data); err != nil {
		return fmt.Errorf("failed to encode Gob data: %w", err)
	}
	if err := temporary.Sync(); err != nil {
		return fmt.Errorf("failed to sync temporary Gob file: %w", err)
	}
	if err := temporary.Close(); err != nil {
		return fmt.Errorf("failed to close temporary Gob file: %w", err)
	}
	closed = true
	if err := os.Rename(temporaryName, filename); err != nil {
		return fmt.Errorf("failed to atomically replace Gob file: %w", err)
	}

	if err := syncDirectory(directory); err != nil {
		return &GobWriteCommittedError{Err: fmt.Errorf("failed to sync Gob directory: %w", err)}
	}

	return nil
}

func syncGobDirectory(directory string) error {
	directoryHandle, err := os.Open(directory)
	if err != nil {
		return fmt.Errorf("failed to open Gob directory for sync: %w", err)
	}
	defer directoryHandle.Close()
	return directoryHandle.Sync()
}
