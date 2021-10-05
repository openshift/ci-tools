import * as React from 'react';
import {Flex, FlexItem, PageSection, Text, TextContent, TextVariants,} from '@patternfly/react-core';

const Home: React.FunctionComponent = () => {
  return <PageSection>
    <Flex direction={{default: 'column'}}
          justifyContent={{default: 'justifyContentSpaceAround'}}
          alignContent={{default: 'alignContentFlexStart'}}>
      <FlexItem>
        <TextContent>
          <Text component={TextVariants.h1}>Repo Initializer</Text>
          <Text component={TextVariants.h2}>What it is/isn&apos;t</Text>
          <Text component={TextVariants.p}>This is a tool designed to streamline the on-boarding of new repositories to
            the CI Platform. Currently, this tool only supports creating new
            repositories and should handle most base cases. In the future, this tool will also support updating existing
            configurations as well ass creating/updating step-registry components (workflows, chains, steps). Please use
            the <a href="https://docs.ci.openshift.org/docs/" target="_blank" rel="noreferrer">CI Operator</a> documentation as a
            companion guide to this tool, or if you have any more advanced cases that
            are currently not supported.</Text>
          <Text component={TextVariants.h2}>How it works</Text>
          <Text component={TextVariants.p}>Using a wizard format, you can provide details about your repository, such as
            basic build settings (what version of Go, does it need to test on a specific cloud provider), in addition to
            specifying any tests that should run. If you&apos;re on-boarding an Operator that is built using the Operator
            SDK, then
            there are some built-in CI workflows that can execute the scorecard tests for you.
          </Text>
          <Text component={TextVariants.p}>At the end, the tool will generate the CI Operator config for out and push it
            to Git - you can at that point copy the YAML and do with it as you please, or the tool can also
            automatically create
            a pull request to the openshift/release repository for this new configuration.
          </Text>
          <Text component={TextVariants.p}><strong>Note:</strong> You will need to fork the <a
            href="https://github.com/openshift/release" target="blank" rel="noreferrer">release</a> repository into your own GitHub
            account in order to use the config generation functionality, as it does interact with GitHub and assumes
            that you have this repository.</Text>
        </TextContent>
      </FlexItem>
    </Flex>
  </PageSection>;
}

export {Home};
