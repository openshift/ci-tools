package main

import (
	"encoding/csv"
	"os"

	"github.com/sirupsen/logrus"
)

type passwdFile struct {
	file string
}

func (p *passwdFile) Validate(user string, password string) bool {
	if user == "" && password == "" {
		logrus.Warn("attempting to validate empty username and password")
		return false
	}
	if p.file == "" {
		return false
	}
	reader, err := os.Open(p.file)
	if err != nil {
		logrus.WithField("file", p.file).Error("failed to open file")
		return false
	}
	defer func() {
		if err := reader.Close(); err != nil {
			logrus.Warn("failed to close file reader")
		}
	}()

	csvReader := csv.NewReader(reader)
	csvReader.Comma = ':'
	csvReader.Comment = '#'
	csvReader.TrimLeadingSpace = true

	records, err := csvReader.ReadAll()
	if err != nil {
		logrus.Error("failed to read csv")
	}

	for _, record := range records {
		if len(record) == 2 && record[0] == user && record[1] == password {
			return true
		}
	}
	return false
}

type literal struct {
	username string
	password func() []byte
}

func (l *literal) Validate(username, password string) bool {
	if username == "" && password == "" {
		logrus.Warn("attempting to validate empty username and password")
		return false
	}
	if l.password == nil {
		return false
	}
	return username == l.username && password == string(l.password())
}

type multi struct {
	delegates []validator
}

func (m *multi) Validate(username, password string) bool {
	for _, delegate := range m.delegates {
		if delegate.Validate(username, password) {
			return true
		}
	}
	return false
}
