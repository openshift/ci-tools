package bumper

import (
	"bytes"
	"fmt"
	"io/fs"
	"io/ioutil"
	"os"
	"path"
	"strings"

	"gopkg.in/ini.v1"

	"github.com/openshift/ci-tools/pkg/api/ocplifecycle"
)

type (
	RepoBumper struct {
		GlobPattern string
		FilesDir    string
		OCPRelease  ocplifecycle.MajorMinor
	}

	RepoBumperOptions struct {
		GlobPattern   string
		FilesDir      string
		CurOCPRelease string
	}

	rhelRepo struct {
		BaseUrl           string `ini:"baseurl,omitempty"`
		Enabled           int    `ini:"baseurl,omitempty"`
		FailoverMethod    bool   `ini:"failovermethod,omitempty"`
		GPGCheck          int    `ini:"gpgcheck,omitempty"`
		GPGKey            string `ini:"gpgkey,omitempty"`
		Name              string `ini:"name,omitempty"`
		PasswordFile      string `ini:"password_file,omitempty"`
		SkipIfUnavailable bool   `ini:"skip_if_unavailable,omitempty"`
		SSLClientCert     string `ini:"sslclientcert,omitempty"`
		SSLClientKey      string `ini:"sslclientkey,omitempty"`
		SSLVerify         bool   `ini:"sslverify,omitempty"`
		UsernameFile      string `ini:"username_file,omitempty"`
	}
)

var (
	majorMinorSeparators = []string{".", "-"}

	_ Bumper[*ini.File] = &RepoBumper{}
)

func NewRepoBumper(o *RepoBumperOptions) (*RepoBumper, error) {
	mm, err := ocplifecycle.ParseMajorMinor(o.CurOCPRelease)
	if err != nil {
		return nil, fmt.Errorf("parse release: %w", err)
	}
	return &RepoBumper{
		GlobPattern: o.GlobPattern,
		FilesDir:    o.FilesDir,
		OCPRelease:  *mm,
	}, nil
}

func (b *RepoBumper) GetFiles() ([]string, error) {
	dirFs := os.DirFS(b.FilesDir)
	matches, err := fs.Glob(dirFs, b.GlobPattern)
	if err != nil {
		return nil, err
	}
	files := make([]string, 0, len(matches))
	for _, f := range matches {
		fileFullPath := path.Join(b.FilesDir, f)
		files = append(files, fileFullPath)
	}
	return files, nil
}

func (b *RepoBumper) Unmarshall(file string) (*ini.File, error) {
	return ini.Load(file)
}

func (b *RepoBumper) BumpFilename(filename string, _ *ini.File) (string, error) {
	curRelease := fmt.Sprintf("%d.%d", b.OCPRelease.Major, b.OCPRelease.Minor)
	futureRelease := fmt.Sprintf("%d.%d", b.OCPRelease.Major, b.OCPRelease.Minor+1)
	return strings.ReplaceAll(filename, curRelease, futureRelease), nil
}

func (b *RepoBumper) BumpContent(file *ini.File) (*ini.File, error) {
	for _, section := range file.Sections() {
		repo := rhelRepo{}
		if err := section.MapTo(&repo); err != nil {
			return nil, err
		}

		for _, s := range majorMinorSeparators {
			curRelease := fmt.Sprintf("%d%s%d", b.OCPRelease.Major, s, b.OCPRelease.Minor)
			futureRelease := fmt.Sprintf("%d%s%d", b.OCPRelease.Major, s, b.OCPRelease.Minor+1)
			repo.BaseUrl = strings.ReplaceAll(repo.BaseUrl, curRelease, futureRelease)
		}

		if err := section.ReflectFrom(&repo); err != nil {
			return nil, err
		}
	}
	return file, nil
}

func (b *RepoBumper) Marshall(file *ini.File, bumpedFilename, dir string) error {
	filePath := path.Join(dir, bumpedFilename)
	return saveIniFile(filePath, file)
}

func saveIniFile(path string, f *ini.File) error {
	ini.PrettySection = true
	ini.PrettyFormat = false
	ini.PrettyEqual = true

	// What follow should have be avoided by using f.SaveTo(path) directly,
	// but unfortunately it appends a double '\n' at the end of the file
	// that makes it different from the original one: we should only bump the fields of
	// interest without doing anything else.
	// Consider opening a PR that fixes this issue, even if I'm not sure this can be
	// considered an issue.
	buf := bytes.NewBuffer(nil)
	if _, err := f.WriteTo(buf); err != nil {
		return err
	}
	bs := buf.Bytes()

	doubleNewLine := ini.LineBreak + ini.LineBreak
	if strings.HasSuffix(string(bs), doubleNewLine) {
		bs = bs[0 : len(bs)-len(ini.LineBreak)]
	}
	if err := ioutil.WriteFile(path, bs, 0666); err != nil {
		return err
	}

	return nil
}
