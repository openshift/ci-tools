package junit

import (
	"encoding/xml"
	"testing"
)

const junitXML = `<testsuites>
<testsuite tests="1" failures="0" time="1983" name="">
<properties>
<property name="go.version" value="go1.17.5 linux/amd64"/>
</properties>
<testcase classname="" name="TestUpgradeControlPlane" time="1983"/>
</testsuite>
</testsuites>`

func Test_CanUnmarshalTestSuites(t *testing.T) {
	suites := &TestSuites{}
	if err := xml.Unmarshal([]byte(junitXML), suites); err != nil {
		t.Fatalf("could not unmarshal: %s", err.Error())
	}
}
