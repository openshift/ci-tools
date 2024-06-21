package junit

import "sigs.k8s.io/prow/pkg/secretutil"

// CensorTestSuite censors secret data in user-provided fields of a jUnit test suite.
func CensorTestSuite(censor secretutil.Censorer, testSuite *TestSuite) {
	if testSuite == nil {
		return
	}
	testSuite.Name = censored(censor, testSuite.Name)
	for i := range testSuite.Properties {
		testSuite.Properties[i].Name = censored(censor, testSuite.Properties[i].Name)
		testSuite.Properties[i].Value = censored(censor, testSuite.Properties[i].Value)
	}
	for i := range testSuite.TestCases {
		testSuite.TestCases[i].Name = censored(censor, testSuite.TestCases[i].Name)
		if testSuite.TestCases[i].SkipMessage != nil {
			testSuite.TestCases[i].SkipMessage.Message = censored(censor, testSuite.TestCases[i].SkipMessage.Message)
		}
		if testSuite.TestCases[i].FailureOutput != nil {
			testSuite.TestCases[i].FailureOutput.Output = censored(censor, testSuite.TestCases[i].FailureOutput.Output)
			testSuite.TestCases[i].FailureOutput.Message = censored(censor, testSuite.TestCases[i].FailureOutput.Message)
		}
		testSuite.TestCases[i].SystemOut = censored(censor, testSuite.TestCases[i].SystemOut)
		testSuite.TestCases[i].SystemErr = censored(censor, testSuite.TestCases[i].SystemErr)
	}
	for i := range testSuite.Children {
		CensorTestSuite(censor, testSuite.Children[i])
	}
}

func censored(censor secretutil.Censorer, value string) string {
	raw := []byte(value)
	censor.Censor(&raw)
	return string(raw)
}
