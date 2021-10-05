import React, {useContext, useState} from 'react';
import {Button} from '@patternfly/react-core';
import {AuthContext, ConfigContext, WizardContext} from "@app/types";
import {fetchWithTimeout, marshallConfig} from "@app/utils/utils";
import {ConfigEditor} from "@app/ConfigEditor/ConfigEditor";
import {ErrorMessage, SuccessMessage} from "@app/Common/Messaging";

const Generate: React.FunctionComponent = () => {
  const authContext = useContext(AuthContext);
  const context = useContext(WizardContext);
  const configContext = useContext(ConfigContext);
  const [isLoading, setIsLoading] = useState(false);
  const [successMessage, setSuccessMessage] = useState("");

  function submit(generatePR: boolean) {
    setIsLoading(true);
    fetchWithTimeout(process.env.API_URI + '/configs?generatePR=' + generatePR, {
      method: 'POST',
      headers: {
        'Accept': 'application/json',
        'Content-Type': 'application/json',
        'access_token': authContext.userData.token,
        'github_user': authContext.userData.userName
      },
      body: JSON.stringify(marshallConfig(configContext.config))
    })
      .then((r) => {
        if (r.status === 200) {
          context.setStep({...context.step, errorMessage: "", stepIsComplete: true});
          r.text()
            .then(text => {
              if (generatePR) {
                setSuccessMessage("Config and Pull Request created!")
              } else {
                setSuccessMessage("Config created: " + text);
              }
              context.setStep({
                ...context.step,
                errorMessages: [],
                stepIsComplete: true
              });
            })
            .catch(() => {
              generateError();
              setIsLoading(false);
            });
        } else {
          generateError();
          setIsLoading(false);
        }
        setIsLoading(false);
      })
      .catch(() => {
        generateError();
        setIsLoading(false);
      });
  }

  function generateError() {
    context.setStep({
      ...context.step,
      errorMessages: ["An error was caught while generating the config."],
      stepIsComplete: false
    });
  }

  return <React.Fragment>
    Does this look ok?
    <ConfigEditor readOnly={true}/>
    <ErrorMessage messages={context.step.errorMessages}/>
    <SuccessMessage message={successMessage}/>
    <Button
      variant="primary"
      isLoading={isLoading}
      spinnerAriaValueText={isLoading ? 'Loading' : undefined}
      onClick={() => submit(false)}>Generate Configuration</Button>
    <Button
      variant="primary"
      isLoading={isLoading}
      spinnerAriaValueText={isLoading ? 'Loading' : undefined}
      onClick={() => submit(true)}>Generate Configuration and Pull Request</Button>
  </React.Fragment>
}

export {Generate}
