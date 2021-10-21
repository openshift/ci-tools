import React, {useContext, useState} from "react";
import {TableComposable, Tbody, Td, Th, Thead, Tr} from "@patternfly/react-table";
import {
  ActionGroup,
  Button,
  Card,
  CardBody,
  CardTitle,
  FormGroup,
  Text,
  TextContent,
  TextInput,
  TextVariants
} from "@patternfly/react-core";
import {AuthContext, ConfigContext, Image, WizardContext} from "@app/types";
import {ErrorMessage} from "@app/Common/Messaging";
import {validateConfig} from "@app/utils/utils";
import _ from "lodash";

const BaseImages: React.FunctionComponent = () => {
  const columns = ['Name', 'Namespace', 'Tag', '']
  const authContext = useContext(AuthContext);
  const configContext = useContext(ConfigContext);
  const context = useContext(WizardContext);

  const [curImage, setCurImage] = useState({} as Image)
  const [errorMessage, setErrorMessage] = useState([] as string[])

  function handleChange(val, evt) {
    const updated = {...curImage}
    _.set(updated, evt.target.name, val);
    setCurImage(updated);
  }

  function saveImage() {
    if (curImage.name && curImage.namespace && curImage.tag) {
      const config = configContext.config;
      const buildConfig = config.buildSettings;
      if (buildConfig) {
        if (!buildConfig.baseImages) {
          buildConfig.baseImages = [];
        }
        if (buildConfig.baseImages.find(t => (t.name.toLowerCase() === curImage.name.toLowerCase()) && (t.namespace.toLowerCase() === curImage.namespace.toLowerCase()) && (t.tag.toLowerCase() === curImage.tag.toLowerCase())) === undefined) {
          const updatedBaseImages = config.buildSettings.baseImages.concat(curImage);
          const updatedConfig = {...config, buildSettings: {...config.buildSettings, baseImages: updatedBaseImages}};
          validate(updatedConfig);
        } else {
          setErrorMessage(["That base image already exists"])
        }
      }
    } else {
      setErrorMessage(["Please provide a name, namespace, and tag for the image."])
    }
  }

  function validate(validationConfig) {
    validateConfig('BASE_IMAGES', validationConfig, authContext.userData, {})
      .then((validationState) => {
        if (validationState.valid) {
          setErrorMessage([]);
          configContext.setConfig(validationConfig);
          setCurImage({} as Image);
          context.setStep({
            ...context.step,
            errorMessages: [],
            stepIsComplete: true
          });
        } else {
          setErrorMessage(validationState.errors != undefined ? validationState.errors.map(error => error.message) : [""]);
          context.setStep({
            ...context.step,
            stepIsComplete: false
          });
        }
      })
  }

  function removeImage(index) {
    const config = configContext.config;
    const buildConfig = config.buildSettings;
    if (buildConfig && buildConfig.baseImages) {
      const updatedBaseImages = [...buildConfig.baseImages];
      updatedBaseImages.splice(index, 1);
      const updatedConfig = {...config, buildSettings: {...config.buildSettings, baseImages: updatedBaseImages}};
      configContext.setConfig(updatedConfig);
    }
  }

  const SummaryTable = () => {
    if (configContext.config.buildSettings.baseImages && configContext.config.buildSettings.baseImages.length > 0) {
      return (<TableComposable aria-label="Base Images">
        <Thead>
          <Tr>
            {columns.map((column, columnIndex) => (
              <Th key={columnIndex}>{column}</Th>
            ))}
          </Tr>
        </Thead>
        <Tbody>
          {configContext.config.buildSettings?.baseImages?.map((row, rowIndex) => (
            <Tr key={rowIndex}>
              <Td key={`${rowIndex}_${0}`} dataLabel={columns[0]}>
                {row.name}
              </Td>
              <Td key={`${rowIndex}_${1}`} dataLabel={columns[1]}>
                {row.namespace}
              </Td>
              <Td key={`${rowIndex}_${2}`} dataLabel={columns[2]}>
                {row.tag}
              </Td>
              <Td key={`${rowIndex}_${3}`} dataLabel={columns[3]}>
                <ActionGroup>
                  <Button variant="danger" onClick={() => removeImage(rowIndex)}>Delete</Button>
                </ActionGroup>
              </Td>
            </Tr>
          ))}
        </Tbody>
      </TableComposable>);
    } else {
      return <React.Fragment/>
    }
  }

  return <Card>
    <CardTitle>Base Images</CardTitle>
    <CardBody>
      <TextContent>
        <Text component={TextVariants.p}>This provides a mapping of named <i>ImageStreamTags</i> which will be available
          for use in container image builds.
          See <a href="https://docs.ci.openshift.org/docs/architecture/ci-operator/#configuring-inputs" target="_blank" rel="noreferrer">Configuring
            Inputs</a>.</Text>
      </TextContent>
      <br/>
      <FormGroup
        label="Image Name"
        fieldId="name">
        <TextInput
          name="name"
          id="name"
          value={curImage.name || ""}
          onChange={handleChange}
        />
      </FormGroup>
      <FormGroup
        label="Image Namespace"
        fieldId="namespace">
        <TextInput
          name="namespace"
          id="namespace"
          value={curImage.namespace || ""}
          onChange={handleChange}
        />
      </FormGroup>
      <FormGroup
        label="Image Tag"
        fieldId="tag">
        <TextInput
          name="tag"
          id="tag"
          value={curImage.tag || ""}
          onChange={handleChange}
        />
      </FormGroup>
      <br/>
      <ErrorMessage messages={errorMessage}/>
      <ActionGroup>
        <Button variant="primary" onClick={saveImage}>Add Base Image</Button>
      </ActionGroup>
      <br/>
      {SummaryTable()}
    </CardBody>
  </Card>
}

export {BaseImages}
