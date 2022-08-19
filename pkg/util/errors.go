package util

import (
	"fmt"
	"strings"
)

func AppendLogToError(err error, log string) error {
	log = strings.TrimSpace(log)
	if len(log) == 0 {
		return err
	}
	return fmt.Errorf("%w\n\n%s", err, log)
}
