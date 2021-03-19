package secrets

import (
	"sync"

	"github.com/sirupsen/logrus"

	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/test-infra/prow/logrusutil"
)

// DynamicCensor keeps a list of censored secrets that is dynamically updated.
// Used when the list of secrets to censor is updated during the execution of
// the program and cannot be determined in advance.  Access to the list of
// secrets is internally synchronized.
type DynamicCensor struct {
	sync.RWMutex
	secrets sets.String
}

func NewDynamicCensor() DynamicCensor {
	return DynamicCensor{
		secrets: sets.NewString(),
	}
}

// AddSecrets adds the content of one or more secrets to the censor list.
func (c *DynamicCensor) AddSecrets(s ...string) {
	c.Lock()
	defer c.Unlock()
	c.secrets.Insert(s...)
}

// Formatter creates a new formatter to be used to filter output.
func (c *DynamicCensor) Formatter(f logrus.Formatter) logrus.Formatter {
	return logrusutil.NewCensoringFormatter(f, func() sets.String {
		return c.secrets
	})
}
