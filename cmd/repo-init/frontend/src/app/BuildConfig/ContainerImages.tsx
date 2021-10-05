import React, {useContext, useState} from "react";
import {
  ActionGroup,
  Button,
  ButtonVariant,
  Card,
  CardBody,
  CardTitle,
  FormGroup,
  Text,
  TextContent,
  TextInput,
  TextVariants
} from "@patternfly/react-core";
import {AuthContext, ConfigContext, ContainerImage, ContainerImageInput, WizardContext} from "@app/types";
import {ErrorMessage} from "@app/Common/Messaging";
import {validateConfig} from "@app/utils/utils";
import {EditableTable} from "@app/Common/EditableTable";
import _ from "lodash";
import {TableComposable, Tbody, Td, Th, Thead, Tr} from "@patternfly/react-table";

const ContainerImages: React.FunctionComponent = () => {
  const columns = ["Name", "From", "Dockerfile", ""]
  const authContext = useContext(AuthContext);
  const context = useContext(WizardContext);
  const configContext = useContext(ConfigContext);

  const [curImage, setCurImage] = useState({inputs: [{}]} as ContainerImage);
  const [errorMessage, setErrorMessage] = useState([] as string[]);
  const [curImageIndex, setCurImageIndex] = useState(-1);

  function handleChange(val, evt) {
    const updated = {...curImage}
    _.set(updated, evt.target.name, val);
    setCurImage(updated);
  }

  function handleContainerImageInputChange(property, val) {
    const updated = {...curImage}
    _.set(updated, property, val);
    setCurImage(updated);
  }

  function addContainerInputImage() {
    const updated = {...curImage}
    if (updated.inputs == undefined) {
      updated.inputs = [];
    }
    updated.inputs.push({} as ContainerImageInput);
    setCurImage(updated);
  }

  function removeContainerInputImage(index) {
    curImage.inputs?.splice(index, 1);
  }

  function saveImage() {
    if (curImage.name && curImage.from && curImage.dockerfile) {
      const config = configContext.config;
      const buildConfig = config.buildSettings;
      if (buildConfig) {
        if (!buildConfig.containerImages) {
          buildConfig.containerImages = [];
        }

        if (curImageIndex != -1 || buildConfig.containerImages.find(t => (t.name.toLowerCase() === curImage.name.toLowerCase())) === undefined) {
          const updatedContainerImages = [...buildConfig.containerImages];
          if (curImageIndex === -1) {
            updatedContainerImages.push(curImage);
          } else {
            updatedContainerImages.splice(curImageIndex, 1, curImage);
          }
          const updatedConfig = {...config, buildSettings: {...buildConfig, containerImages: updatedContainerImages}};
          validate(updatedConfig);
        } else {
          setErrorMessage(["That container image already exists"])
        }
      }
    } else {
      setErrorMessage(["Please provide a name, from, and dockerfile for the image."])
    }
  }

  function validate(validationConfig,) {
    validateConfig("CONTAINER_IMAGES", validationConfig, authContext.userData, {})
      .then((validationState) => {
        if (validationState.valid) {
          configContext.setConfig(validationConfig);
          setErrorMessage([]);
          setCurImage({} as ContainerImage);
          setCurImageIndex(-1);
          context.setStep({...context.step, errorMessages: [], stepIsComplete: true});
        } else {
          setErrorMessage(validationState.errors != undefined ? validationState.errors.map(error => error.message) : [""]);
          context.setStep({
            ...context.step,
            stepIsComplete: false
          });
        }
      })
  }

  function editImage(index) {
    const image = configContext.config.buildSettings.containerImages?.[index];

    if (image) {
      setCurImage(image);
      setCurImageIndex(index);
    }
  }

  function removeImage(index) {
    const config = configContext.config;
    const buildConfig = config.buildSettings;
    if (buildConfig && buildConfig.containerImages) {
      const updatedContainerImages = [...buildConfig.containerImages];
      updatedContainerImages.splice(index, 1);
      const updatedConfig = {
        ...config,
        buildSettings: {...config.buildSettings, containerImages: updatedContainerImages}
      };
      configContext.setConfig(updatedConfig);
    }
  }

  const defaultActions = [
    {
      title: "Delete",
      onClick: (event, rowId) => removeContainerInputImage(rowId),
      isOutsideDropdown: true
    },
  ];

  const lastRowActions = [
    {
      title: "Add",
      variant: ButtonVariant.secondary,
      onClick: () => addContainerInputImage(),
      isOutsideDropdown: true
    },
  ]

  const ImageInputs = () => {
    return (<React.Fragment>
      <TextContent>
        <Text component={TextVariants.h5}>Dockerfile Image Replacements</Text>
        <Text component={TextVariants.p}>Here, you may enter image tag replacements for the Container Image Dockerfile.
          For example,
          if you&apos;d like to replace <i>FROM registry.ci.openshift.org/ocp/builder:golang-1.13 AS builder</i> in the
          Dockerfile with
          an imported image, e.g. <i>bin</i>, this is where you&apos;d do that.
        </Text>
      </TextContent>
      <EditableTable
        actions={{
          items: defaultActions
        }}
        lastRowActions={{
          items: lastRowActions
        }}
        collectionName="inputs"
        columns={[
          {
            name: "Name",
            property: "name"
          },
          {
            name: "Replaces",
            property: "replaces"
          },
        ]}
        data={curImage.inputs}
        onChange={handleContainerImageInputChange}/>
    </React.Fragment>);
  };

  const ContainerImagesTable = () => {
    if (configContext.config.buildSettings.containerImages && configContext.config.buildSettings.containerImages.length > 0) {
      return (
        <TableComposable aria-label="Container Images">
          <Thead>
            <Tr>
              {columns.map((column, columnIndex) => (
                <Th key={columnIndex}>{column}</Th>
              ))}
            </Tr>
          </Thead>
          <Tbody>
            {configContext.config.buildSettings?.containerImages?.map((row, rowIndex) => (
              <Tr key={rowIndex}>
                <Td key={`${rowIndex}_${0}`} dataLabel={columns[0]}>
                  {row.name}
                </Td>
                <Td key={`${rowIndex}_${1}`} dataLabel={columns[1]}>
                  {row.from}
                </Td>
                <Td key={`${rowIndex}_${2}`} dataLabel={columns[2]}>
                  {row.literalDockerfile ? "(Dockerfile Literal...)" : row.dockerfile}
                </Td>
                <Td key={`${rowIndex}_${3}`} dataLabel={columns[3]}>
                  <ActionGroup>
                    <Button variant="primary" onClick={() => editImage(rowIndex)}>Edit</Button>
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
    <CardTitle>Container Images</CardTitle>
    <CardBody>
      <TextContent>
        <Text component={TextVariants.p}>Here, you may define additional output container images built using the other
          images defined within the config.
          See <a href="https://docs.ci.openshift.org/docs/architecture/ci-operator/#building-container-images"
                 target="_blank" rel="noreferrer">Building Container Images</a>.</Text>
      </TextContent>
      <br/>
      <FormGroup
        label="Image Name"
        fieldId="name">
        <TextInput
          name="name"
          id="name"
          value={curImage.name || ''}
          onChange={handleChange}
        />
      </FormGroup>
      <FormGroup
        label="From"
        fieldId="from">
        <TextInput
          name="from"
          id="from"
          value={curImage.from || ''}
          onChange={handleChange}
        />
      </FormGroup>
      <FormGroup
        label="Dockerfile"
        fieldId="dockerfile">
        <TextInput
          name="dockerfile"
          id="dockerfile"
          value={curImage.dockerfile || ''}
          onChange={handleChange}
        />
      </FormGroup>
      <br/>
      {ImageInputs()}
      <ErrorMessage messages={errorMessage}/>
      <br/>
      <ActionGroup>
        <Button variant="primary" onClick={saveImage}>Save Container Image</Button>
      </ActionGroup>
      <br/>
      {ContainerImagesTable()}
    </CardBody>
  </Card>
}

export {ContainerImages}
