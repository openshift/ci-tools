package junit

import "sigs.k8s.io/prow/pkg/secretutil"

// CensorTestSuite censors secret data in user-provided fields of a jUnit test suite.
func CensorTestSuite(censor secretutil.Censorer, testSuite *TestSuite) {
	if testSuite == nil {
		return
	}
	censorStr(censor, &testSuite.Name)
	for i, prop := range testSuite.Properties {
		censorStr(censor, &prop.Name, &prop.Value)
		testSuite.Properties[i] = prop
	}
	for i, testCase := range testSuite.TestCases {
		censorStr(censor, &testCase.Name)
		for j, prop := range testCase.Properties {
			censorStr(censor, &prop.Name, &prop.Value)
			testCase.Properties[j] = prop
		}
		if testCase.SkipMessage != nil {
			censorStr(censor, &testCase.SkipMessage.Message)
		}
		if testCase.FailureOutput != nil {
			censorStr(censor, &testCase.FailureOutput.Output, &testCase.FailureOutput.Message)
		}
		censorStr(censor, &testCase.SystemOut, &testCase.SystemErr)
		testSuite.TestCases[i] = testCase
	}
	for i := range testSuite.Children {
		CensorTestSuite(censor, testSuite.Children[i])
	}
}

func censorStr(censor secretutil.Censorer, values ...*string) {
	for _, val := range values {
		raw := []byte(*val)
		censor.Censor(&raw)
		*val = string(raw)
	}
}
