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
	return fmt.Errorf("%s\n\n%s", err.Error(), log)
}
