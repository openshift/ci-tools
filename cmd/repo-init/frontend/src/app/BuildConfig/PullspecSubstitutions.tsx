import React, {useContext, useState} from "react";
import {TableComposable, Tbody, Td, Th, Thead, Tr} from "@patternfly/react-table";
import {ActionGroup, Button, FormGroup, Text, TextContent, TextInput, TextVariants} from "@patternfly/react-core";
import {AuthContext, ConfigContext, PullspecSubstitution, WizardContext} from "@app/types";
import {ErrorMessage} from "@app/Common/Messaging";
import {validateConfig} from "@app/utils/utils";
import _ from "lodash";

const PullspecSubstitutions: React.FunctionComponent = () => {
  const columns = ['Pullspec', 'With', '']
  const authContext = useContext(AuthContext);
  const context = useContext(WizardContext);
  const configContext = useContext(ConfigContext);

  const [curSubstitution, setCurSubstitution] = useState({} as PullspecSubstitution)
  const [errorMessage, setErrorMessage] = useState([] as string[])

  function handleChange(val, evt) {
    const updated = {...curSubstitution}
    _.set(updated, evt.target.name, val);
    setCurSubstitution(updated);
  }

  function saveSubstitution() {
    const config = configContext.config;
    let operatorConfig = config.buildSettings?.operatorConfig;
    if (!operatorConfig) {
      operatorConfig = {isOperator: true, substitutions: []}
    }
    if (operatorConfig.substitutions.find(t => (t.pullspec.toLowerCase() === curSubstitution.pullspec.toLowerCase())) === undefined) {
      const substitutionObj = {substitution: {...curSubstitution}}
      validate(config, substitutionObj, () => {
        operatorConfig.substitutions.push(curSubstitution);
        configContext.setConfig(config);
        setCurSubstitution({} as PullspecSubstitution);
      });
    } else {
      context.setStep({...context.step, errorMessages: ["A substitution for that pullspec already exists"]});
    }
  }

  function validate(validationConfig, substitution, onSuccess) {
    validateConfig('OPERATOR_SUBSTITUTION', validationConfig, authContext.userData, substitution)
      .then((validationState) => {
        if (validationState.valid) {
          onSuccess();
          setErrorMessage([]);
          context.setStep({...context.step, errorMessage: "", stepIsComplete: true});
        } else {
          setErrorMessage(validationState.errors != undefined ? validationState.errors.map(error => error.message) : [""]);
          context.setStep({
            ...context.step,
            stepIsComplete: false
          });
        }
      })
  }

  function removeSubstitution(index) {
    const config = configContext.config;
    const operatorConfig = config.buildSettings?.operatorConfig;
    if (operatorConfig) {
      operatorConfig.substitutions.splice(index, 1);
      configContext.setConfig(config);
    }
  }

  const SummaryTable = () => {
    if (configContext.config.buildSettings.operatorConfig?.substitutions && configContext.config.buildSettings.operatorConfig?.substitutions?.length > 0) {
      return (<TableComposable aria-label="Pullspec Substitutions">
        <Thead>
          <Tr>
            {columns.map((column, columnIndex) => (
              <Th key={columnIndex}>{column}</Th>
            ))}
          </Tr>
        </Thead>
        <Tbody>
          {configContext.config.buildSettings?.operatorConfig?.substitutions?.map((row, rowIndex) => (
            <Tr key={rowIndex}>
              <Td key={`${rowIndex}_${0}`} dataLabel={columns[0]}>
                {row.pullspec}
              </Td>
              <Td key={`${rowIndex}_${1}`} dataLabel={columns[1]}>
                {row.with}
              </Td>
              <Td key={`${rowIndex}_${2}`} dataLabel={columns[2]}>
                <ActionGroup>
                  <Button variant="danger" onClick={() => removeSubstitution(rowIndex)}>Delete</Button>
                </ActionGroup>
              </Td>
            </Tr>
          ))}
        </Tbody>
      </TableComposable>);
    } else {
      return <React.Fragment/>;
    }
  }

  return (
    <React.Fragment>
      <TextContent>
        <Text component={TextVariants.h5}><strong>Pullspec Substitutions</strong></Text>
      </TextContent>
      <br/>
      <FormGroup
        label="Pullspec to replace"
        fieldId="pullspec">
        <TextInput
          name="pullspec"
          id="pullspec"
          value={curSubstitution.pullspec || ''}
          onChange={handleChange}
        />
      </FormGroup>
      <FormGroup
        label="What should the pullspec be replaced with?"
        fieldId="with">
        <TextInput
          name="with"
          id="with"
          value={curSubstitution.with || ''}
          onChange={handleChange}
        />
      </FormGroup>
      <br/>
      <ErrorMessage messages={errorMessage}/>
      <ActionGroup>
        <Button variant="primary" onClick={saveSubstitution}>Add Substitution</Button>
      </ActionGroup>
      <br/>
      {SummaryTable()}
    </React.Fragment>)
}

export {PullspecSubstitutions}
