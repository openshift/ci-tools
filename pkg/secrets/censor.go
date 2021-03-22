package secrets

import (
	"sync"

	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/test-infra/prow/secretutil"
)

// DynamicCensor keeps a list of censored secrets that is dynamically updated.
// Used when the list of secrets to censor is updated during the execution of
// the program and cannot be determined in advance.  Access to the list of
// secrets is internally synchronized.
type DynamicCensor struct {
	sync.RWMutex
	*secretutil.ReloadingCensorer
	secrets sets.String
}

func NewDynamicCensor() DynamicCensor {
	return DynamicCensor{
		ReloadingCensorer: secretutil.NewCensorer(),
		secrets:           sets.NewString(),
	}
}

// AddSecrets adds the content of one or more secrets to the censor list.
func (c *DynamicCensor) AddSecrets(s ...string) {
	c.Lock()
	defer c.Unlock()
	c.secrets.Insert(s...)
	c.ReloadingCensorer.Refresh(c.secrets.List()...)
}
