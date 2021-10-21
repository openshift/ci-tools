import ReactHtmlParser from 'react-html-parser';

export const testTypeHelpText = () => {
  return ReactHtmlParser('<div><strong><u>Unit:</u></strong> These test scripts execute unit or integration style tests by running a command from your repository inside of a test container. ' +
    'For example, a unit test may be executed by running \'make test-unit\' after checking out the code under test<br/><br/>' +
    '<strong><u>E2E:</u></strong> An end-to-end test executes a command from your repository against an ephemeral OpenShift cluster.' +
    'The test script will have \'cluster:admin\' credentials with which it can execute no other tests will share the cluster.<br/><br/>' +
    '<strong><u>Operator:</u></strong> (Only Available to Optional Operators) Similarly to an end-to-end test, an operator test executes against an ephemeral OpenShift cluster. ' +
    'However, an operator test will use canned scorecard enabled workflows that require your operator to be build with the Operator SDK and deployed through OLM. ' +
    'See <a href="https://docs.ci.openshift.org/docs/how-tos/testing-operator-sdk-operators/" target="_blank">Testing Operators</a> for more info.</div>');
}
