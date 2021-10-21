import React, {useContext, useState} from 'react';
import {Button, Grid, GridItem, Wizard, WizardContextConsumer, WizardFooter} from '@patternfly/react-core';
import {RepoBuildConfig} from "@app/BuildConfig/BuildConfig";
import {RepoInfo} from "@app/RepoInfo/RepoInfo";
import {TestConfig} from "@app/TestConfig/TestConfig";
import {AuthContext, WizardContext, WizardStep} from "@app/types";
import {Generate} from "@app/Generate/Generate";
import {Redirect, useHistory} from "react-router-dom";

const RepoInitWizard: React.FunctionComponent = () => {
  const auth = useContext(AuthContext)
  const [step, setStep] = useState({} as WizardStep);
  const history = useHistory();

  const [stepIdReached, setStepIdReached] = useState(1);

  if (!auth.userData.isAuthenticated) {
    return <Redirect to="/login"/>;
  }

  function onBack(newStep) {
    setStepIdReached(stepIdReached < newStep.id ? newStep.id : stepIdReached);
    setStep({
      step: newStep.id,
      stepIsComplete: false,
      errorMessages: []
    });
  }

  function onNext(newStep) {
    if (step.validator === undefined || step.validator()) {
      setStepIdReached(stepIdReached < newStep.id ? newStep.id : stepIdReached);
      setStep({
        step: newStep.id,
        stepIsComplete: false,
        errorMessages: []
      });
    } else {
      setStep({
        ...step,
        errorMessages: ["STEP DONE BROKENED"],
        stepIsComplete: false
      });
    }
  }

  function goNext(onNext) {
    if (step.stepIsComplete) {
      onNext();
    }
  }

  function launchEditor() {
    if (confirm("Editing the raw config allows you to do things that are not supported in the config wizard. Therefore, if you toggle back and forth" +
      "between the config, the changes you make while editing the raw config will not be updated in the wizard.")) {
      history.push("config-editor");
    }
  }

  const CustomFooter = (
    <WizardFooter>
      <WizardContextConsumer>
        {({activeStep, onNext, onBack}) => {
          if (activeStep.name !== 'Verify') {
            return (
              <div>
                <Button variant="primary" type="submit" isDisabled={!step.stepIsComplete}
                        onClick={() => goNext(onNext)}>
                  Next
                </Button>
                <Button variant="secondary" onClick={onBack}
                        className={activeStep.name === 'Repo Information' ? 'pf-m-disabled' : ''}>
                  Back
                </Button>
                <Button variant="secondary" onClick={launchEditor}>
                  Edit Raw Config
                </Button>
              </div>
            )
          } else {
            // Final step buttons
            return (
              <div>
                <Button variant="secondary" onClick={onBack}
                        className={activeStep.name === 'Repo Information' ? 'pf-m-disabled' : ''}>
                  Back
                </Button>
                <Button variant="secondary" onClick={launchEditor}>
                  Edit Raw Config
                </Button>
              </div>
            )
          }
        }}
      </WizardContextConsumer>
    </WizardFooter>
  );

  const steps = [
    {id: 1, name: 'Repo Information', component: <RepoInfo/>},
    {id: 2, name: 'Build Config', component: <RepoBuildConfig/>, canJumpTo: stepIdReached >= 2},
    {id: 3, name: 'Test Config', component: <TestConfig/>, canJumpTo: stepIdReached >= 3},
    {id: 4, name: 'Generate', component: <Generate/>, canJumpTo: stepIdReached >= 4}
  ];
  const title = 'Repo Config Wizard';
  return (
    <Grid>
      <GridItem span={12} rowSpan={12}>
        <WizardContext.Provider value={{step: step, setStep: setStep}}>
          <Wizard
            navAriaLabel={`${title} steps`}
            mainAriaLabel={`${title} content`}
            steps={steps}
            footer={CustomFooter}
            height="100%"
            onBack={onBack}
            onNext={onNext}/>
        </WizardContext.Provider>
      </GridItem>
    </Grid>
  )
}

export default RepoInitWizard
