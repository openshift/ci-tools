import React, {useContext, useEffect} from 'react';
import {FormGroup, Text, TextContent, TextInput, TextVariants} from '@patternfly/react-core';
import {ConfigContext, WizardContext} from "@app/types";
import {ErrorMessage} from "@app/Common/Messaging"

const RepoInfo: React.FunctionComponent = () => {
  const context = useContext(WizardContext);
  const configContext = useContext(ConfigContext);

  useEffect(() => {
    validate();
  }, []); // eslint-disable-line react-hooks/exhaustive-deps

  const handleChange = (checked, event) => {
    const target = event.target;
    const name = target.name;
    configContext.setConfig({...configContext.config, [name]: target.value});
  };

  function onBlur(evt) {
    const config = configContext.config;
    config[evt.target.name] = evt.target.value;

    validate();
  }

  function validate() {
    const config = configContext.config;
    if (config.org && config.repo && config.branch) {
      fetch('http://localhost:8080/api/configs?org=' + config.org + '&repo=' + config.repo)
        .then((r) => {
          if (r.status === 404) {
            context.setStep({...context.step, errorMessages: [], stepIsComplete: true});
          } else {
            context.setStep({
              ...context.step,
              errorMessages: ["It looks like there's already a configuration for that org and repo combination. This tool currently does not support editing existing configurations, although this will be added in the future."],
              stepIsComplete: false
            });
          }
        })
        .catch(() => {
          context.setStep({
            ...context.step,
            errorMessages: ["An error occurred while validating if this configuration already exists."],
            stepIsComplete: false
          });
        });
    } else {
      context.setStep({
        ...context.step,
        stepIsComplete: false
      });
    }
  }

  return <React.Fragment>
    <TextContent>
      <Text component={TextVariants.h4}>Repository Configuration</Text>
      <Text component={TextVariants.p}>Enter the org, repo name and development branch of the component under
        test.</Text>
    </TextContent>
    <br/>
    <FormGroup
      label="Repo Organization"
      isRequired
      fieldId="org">
      <TextInput
        isRequired
        type="text"
        id="org"
        name="org"
        onBlur={onBlur}
        onChange={handleChange}
        value={configContext.config.org || ''}
      />
    </FormGroup>
    <FormGroup
      label="Repo Name"
      isRequired
      fieldId="repo">
      <TextInput
        isRequired
        type="text"
        id="repo"
        name="repo"
        onBlur={onBlur}
        onChange={handleChange}
        value={configContext.config.repo || ''}
      />
    </FormGroup>
    <FormGroup
      label="Development Branch"
      isRequired
      fieldId="branch">
      <TextInput
        isRequired
        type="text"
        id="branch"
        name="branch"
        onBlur={onBlur}
        onChange={handleChange}
        value={configContext.config.branch || ''}
      />
    </FormGroup>
    <ErrorMessage messages={context.step.errorMessages}/>
  </React.Fragment>


}

export {RepoInfo}
