package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io/fs"
	"io/ioutil"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/sirupsen/logrus"

	"k8s.io/apimachinery/pkg/util/sets"
	"sigs.k8s.io/yaml"
)

func main() {
	ldapFile := flag.String("ldap-file", "", "File generated via ldapsearch -LLL -x -h ldap.corp.redhat.com -b ou=users,dc=redhat,dc=com '(rhatSocialURL=GitHub*)' rhatSocialURL uid")
	repoBaseDir := flag.String("repo-base-dir", "", "base dir for the target repo. Will be used to resolve OWNERS_ALIASES")
	repoSubdir := flag.String("repo-sub-dir", ".", "Subdir relative to the --repo-base-dir to look for OWNERS files")
	flag.Parse()

	ldapUsers, errs := getLdapUsers(*ldapFile, *repoBaseDir, *repoSubdir)
	if len(errs) > 0 {
		for _, err := range errs {
			logrus.WithError(err).Error("encountered error trying to resolve owners")
		}
	}
	serialized, err := json.Marshal(ldapUsers.List())
	if err != nil {
		logrus.WithError(err).Fatal("failed to serialize ldap user list")
	}
	fmt.Fprint(os.Stdout, string(serialized))
}

func getLdapUsers(ldapFile, repoBaseDir, repoSubdir string) (sets.String, []error) {
	mappings, errs := createLDAPMapping(ldapFile)
	if len(errs) > 0 {
		for _, err := range errs {
			logrus.WithError(err).Warn("encountered error trying to parse ldap file")
		}
	}

	return getAllSecretUsers(repoBaseDir, repoSubdir, mappings)
}

func getAllSecretUsers(repoBaseDir, repoSubDir string, mapping map[string]string) (sets.String, []error) {
	ownersAliasesRaw, err := ioutil.ReadFile(repoBaseDir + "/OWNERS_ALIASES")
	if err != nil {
		return nil, []error{fmt.Errorf("failed to read OWNERS_ALIASES: %w", err)}
	}
	var ownersAliases OwnersALISES
	if err := yaml.Unmarshal(ownersAliasesRaw, &ownersAliases); err != nil {
		return nil, []error{fmt.Errorf("failed to unmarshal owners aliases: %w", err)}
	}
	result := sets.String{}
	var errs []error
	l := sync.Mutex{}
	wg := sync.WaitGroup{}
	_ = filepath.WalkDir(repoBaseDir+"/"+repoSubDir, func(path string, info fs.DirEntry, err error) error {
		if filepath.Base(path) != "OWNERS" {
			return nil
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			data, err := ioutil.ReadFile(path)
			if err != nil {
				l.Lock()
				errs = append(errs, fmt.Errorf("failed to read %s: %w", path, err))
				l.Unlock()
				return
			}
			var owners OWNERS
			if err := yaml.Unmarshal(data, &owners); err != nil {
				l.Lock()
				errs = append(errs, fmt.Errorf("failed to unmarshal %s: %w", path, err))
				l.Unlock()
				return
			}
			if len(owners.Approvers) == 0 {
				l.Lock()
				errs = append(errs, fmt.Errorf("owners file %s had zero entries", path))
				l.Unlock()
				return
			}
			var resolvedOwners []string
			for _, approver := range owners.Approvers {
				if val, ok := ownersAliases.Aliases[approver]; ok {
					resolvedOwners = append(resolvedOwners, val...)
				} else {
					resolvedOwners = append(resolvedOwners, approver)
				}
			}
			for _, resolvedOwner := range resolvedOwners {
				ldapUser, found := mapping[strings.ToLower(resolvedOwner)]
				func() {
					l.Lock()
					defer l.Unlock()

					if !found {
						errs = append(errs, fmt.Errorf("didn't find github user %s in ldap mapping", strings.ToLower(resolvedOwner)))
					} else {
						result.Insert(strings.ToLower(ldapUser))
					}
				}()
			}
		}()

		return nil
	})

	wg.Wait()
	return result, errs
}

type OWNERS struct {
	Approvers []string `json:"approvers"`
}

type OwnersALISES struct {
	Aliases map[string][]string `json:"aliases"`
}

func createLDAPMapping(ldapFile string) (map[string]string, []error) {
	data, err := ioutil.ReadFile(ldapFile)
	if err != nil {
		return nil, []error{fmt.Errorf("reading file failed: %w", err)}
	}
	entries := bytes.Split(data, []byte("\n\n"))
	var errs []error
	result := map[string]string{}
	for _, entry := range entries {
		if len(bytes.TrimSpace(entry)) == 0 {
			continue
		}
		lines := bytes.Split(entry, []byte("\n"))
		var ldapUser, gitHubUser string
		for _, line := range lines {
			if bytes.HasPrefix(bytes.ToLower(line), []byte("rhatsocialurl: github->")) {
				slashSplit := strings.Split(string(bytes.ToLower(line)), "/")
				if slashSplit[len(slashSplit)-1] != "" {
					gitHubUser = slashSplit[len(slashSplit)-1]
				} else if slashSplit[len(slashSplit)-2] != "" {
					gitHubUser = slashSplit[len(slashSplit)-2]
				}
			}
			if bytes.HasPrefix(line, []byte("uid: ")) {
				ldapUser = string(bytes.TrimPrefix(line, []byte("uid: ")))
			}
		}
		var errMsg string
		if ldapUser == "" {
			errMsg += "couldn't find LDAP uid"
		}
		if gitHubUser == "" {
			errMsg += "couldn't extract github user"
		}
		if errMsg != "" {
			errs = append(errs, fmt.Errorf("entry ---\n%s\n---\n: %s ", string(entry), errMsg))
			continue
		}
		if _, alreadyExists := result[strings.ToLower(gitHubUser)]; alreadyExists {
			errs = append(errs, fmt.Errorf("found another entry for ldap user %s", gitHubUser))
			continue
		}
		result[strings.ToLower(gitHubUser)] = ldapUser
	}

	return result, errs
}
