import {
  AuthContextInterface,
  CloudProvider,
  ContainerImage,
  Image,
  OperatorConfig,
  PullspecSubstitution, ReleaseType,
  RepoConfig,
  Test,
  TestType, UserData,
  ValidationState
} from "@app/types";

export function accessibleRouteChangeHandler() {
  return window.setTimeout(() => {
    const mainContainer = document.getElementById('primary-app-container');
    if (mainContainer) {
      mainContainer.focus();
    }
  }, 50);
}

export function marshallConfig(config: RepoConfig) {
  return {
    org: config.org,
    repo: config.repo,
    branch: config.branch,
    canonical_go_repository: config.buildSettings.canonicalGoRepository,
    promotes: config.buildSettings.buildPromotes,
    promotes_with_openshift: config.buildSettings.partOfOSRelease,
    needs_base: config.buildSettings.needsBase,
    needs_os: config.buildSettings.needsOS,
    go_version: config.buildSettings.goVersion,
    base_images: marshallBaseImages(config.buildSettings.baseImages),
    images: marshallContainerImages(config.buildSettings.containerImages),
    build_commands: config.buildSettings?.buildCommands,
    test_build_commands: config.buildSettings?.testBuildCommands,
    tests: marshallTests(config.tests, [TestType.Unit]),
    custom_e2e: marshallTests(config.tests, [TestType.E2e, TestType.Operator]),
    operator_bundle: marshallOperator(config.buildSettings?.operatorConfig),
    release_type: config.buildSettings?.release.type !== ReleaseType.No ? config.buildSettings?.release.type.toLowerCase() : "",
    release_version: config.buildSettings?.release.version
  };
}

export function marshallBaseImages(images: Image[] | undefined) {
  let marshalledImages = {};
  if (images !== undefined) {
    images.forEach(image => {
      marshalledImages[image.name] = image;
    })
  }

  return marshalledImages;
}

function marshallContainerImages(images: ContainerImage[] | undefined) {
  return images !== undefined ? images.map(image => marshallContainerImage(image)) : undefined;
}

function marshallContainerImage(image: ContainerImage) {
  let marshalledImage = {
    from: image.from,
    to: image.name
  };

  if (image.literalDockerfile) {
    marshalledImage['dockerfile_literal'] = image.dockerfile;
  } else {
    marshalledImage['dockerfile_path'] = image.dockerfile;
  }

  //Group image inputs by replacement image. It's possible that, for example, the image `image1` is used to replace
  //multiple pullspecs in the Dockerfile, and this is how it is represented on the back-end. For UI purposes it was (IMO)
  //cleaner to represent them separately.
  if (image.inputs !== undefined && image.inputs.length > 0) {
    let grouped = []
    // @ts-ignore
    image.inputs.reduce((prev, curr) => {
      //The last empty row (if present) will be undefined, so skip it.
      if (curr.name !== undefined) {
        if (!grouped[curr.name]) {
          grouped[curr.name] = [];
        }
        grouped[curr.name].push(curr.replaces);
      }

      return grouped;
    })

    let inputs = {}
    Object.keys(grouped).map(function (image, index) {
      inputs[image] = {
        from: image,
        as: grouped[image]
      }
    });

    marshalledImage['inputs'] = inputs;
  }

  return marshalledImage;
}


function marshallTests(tests: Test[], testTypes: TestType[]) {
  let marshalledTests: object[] = [];
  if (tests !== undefined && tests.length > 0) {
    tests.filter(test => testTypes.includes(test.type)).forEach(test => {
      if (test.type === TestType.Unit) {
        marshalledTests.push({
          as: test.name,
          command: test.testCommands,
          from: test.from
        });
      } else if (test.type === TestType.E2e) {
        marshalledTests.push({
          as: getTrimmedVal(test.name),
          command: getTrimmedVal(test.testCommands),
          profile: test.clusterProfile?.toLowerCase(),
          cli: test.requiresCli
        });
      } else if (test.type === TestType.Operator) {
        marshalledTests.push({
          as: getTrimmedVal(test.name),
          command: getTrimmedVal(test.testCommands),
          workflow: determineOOWorkflow(test.cloudProvider),
          cli: test.requiresCli,
          environment: marshallEnvironment(test),
          dependencies: marshallDependencies(test)
        });
      }
    })
  }

  return marshalledTests;
}

function marshallEnvironment(operatorTest: Test) {
  let environment = {};
  if (operatorTest.operatorConfig !== undefined) {
    let operatorConfig = operatorTest.operatorConfig;
    if (operatorConfig.channel !== undefined) {
      environment['OO_CHANNEL'] = getYamlString(operatorConfig.channel);
    }
    if (operatorConfig.package !== undefined) {
      environment['OO_PACKAGE'] = getYamlString(operatorConfig.package);
    }
    if (operatorConfig.installNamespace !== undefined) {
      environment['OO_INSTALL_NAMESPACE'] = getYamlString(operatorConfig.installNamespace);
    }
    if (operatorConfig.targetNamespaces !== undefined) {
      environment['OO_TARGET_NAMESPACES'] = getYamlString(operatorConfig.targetNamespaces);
    }
  }

  return environment;
}

function marshallDependencies(operatorTest: Test) {
  let dependencies = {};
  if (operatorTest.operatorConfig !== undefined) {
    let operatorConfig = operatorTest.operatorConfig;
    if (operatorConfig.bundleName === undefined || operatorConfig.bundleName === "") {
      operatorConfig.bundleName = "ci-index";
    }
    dependencies['OO_INDEX'] = getYamlString(operatorConfig.bundleName);
  }

  return dependencies;
}

function getTrimmedVal(val: string | undefined) {
  return val !== undefined ? val.trim() : ""
}

//Escape special characters with single quotes.
function getYamlString(val: string) {
  val = getTrimmedVal(val);
  if (val.match("/[ `!@#$%^&*()_+\-=\[\\]{};':\"\\|,.<>\/?~]/") !== null) {
    val = "'" + val + "'";
  }

  return val;
}

function marshallOperator(operatorBundle: OperatorConfig | undefined) {
  if (operatorBundle !== undefined && operatorBundle.isOperator) {
    let name = getTrimmedVal(operatorBundle.name);
    return {
      name: name !== "" ? name : "ci-index",
      dockerfile_path: getTrimmedVal(operatorBundle.dockerfilePath),
      context_dir: getTrimmedVal(operatorBundle.contextDir),
      base_index: getTrimmedVal(operatorBundle.baseIndex),
      update_graph: getTrimmedVal(operatorBundle.updateGraph),
      substitutions: marshallOperatorSubstitutions(operatorBundle.substitutions)
    }
  } else {
    return null;
  }
}

function marshallOperatorSubstitutions(substitutions: PullspecSubstitution[]) {
  let marshalledSubstitutions: object[] = [];
  if (substitutions !== undefined && substitutions.length > 0) {
    substitutions.forEach(substitution => {
      marshalledSubstitutions.push({
        pullspec: getTrimmedVal(substitution.pullspec),
        with: getTrimmedVal(substitution.with)
      });
    });
  }

  return marshalledSubstitutions;
}

function determineOOWorkflow(cloudProvider: CloudProvider | undefined) {
  if (cloudProvider === CloudProvider.Aws) {
    return "optional-operators-ci-aws";
  } else if (cloudProvider === CloudProvider.Azure) {
    return "optional-operators-ci-azure";
  } else if (cloudProvider === CloudProvider.Gcp) {
    return "optional-operators-ci-gcp";
  }

  return "optional-operators-ci-aws";
}

export function validateConfig(type: string, config: RepoConfig, userData: UserData, additionalData: object): Promise<ValidationState> {
  let marshalledConfig = marshallConfig(config);

  let request = {
    validation_type: type,
    data: {
      config: marshalledConfig,
      ...additionalData
    }
  }
  return fetch(process.env.API_URI + '/config-validations', {
    method: 'POST',
    // @ts-ignore
    headers: {
      'Accept': 'application/json',
      'Content-Type': 'application/json',
      'access_token': userData.token,
      'github_user': userData.userName
    },
    body: JSON.stringify(request)
  })
    .then((r) => {
      return r.json().then((json) => {
        return json;
      });
    })
    .catch((e) => {
      return {
        valid: false,
        errorMessage: "An error occurred while validating the data."
      }
    });
}

//Convert the config into a real YAML representation of what the ci-operator config would look like.
export function convertConfig(config: RepoConfig, userData: UserData): Promise<string | undefined> {
  let marshalledConfig = marshallConfig(config);
  return fetch(process.env.API_URI + '/configs?conversionOnly=true', {
    method: 'POST',
    // @ts-ignore
    headers: {
      'Content-Type': 'application/json',
      'access_token': userData.token,
      'github_user': userData.userName
    },
    body: JSON.stringify(marshalledConfig)
  })
    .then((r) => {
      return r.text().then((yaml) => {
        return yaml;
      });
    })
    .catch((e) => {
      return undefined;
    });
}

// fetch doesn't support timeouts by default, and this can vary by browser. Chrome has something crazy like 300 seconds.
export async function fetchWithTimeout(resource, options = {}) {
  // @ts-ignore
  const { timeout = 10000 } = options;

  const controller = new AbortController();
  const id = setTimeout(() => controller.abort(), timeout);
  const response = await fetch(resource, {
    ...options,
    signal: controller.signal
  });
  clearTimeout(id);
  return response;
}
