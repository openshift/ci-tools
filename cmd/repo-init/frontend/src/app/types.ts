import { createContext } from 'react';

export const ghAuthState = {
  isAuthenticated: false,
  token: null,
};

export interface ConfigProperties {
  githubApiUrl: string | undefined;
  githubClientId: string | undefined;
  githubRedirectUrl: string | undefined;
  loaded: boolean;
}

export interface ConfigPropertiesContextInterface {
  configProperties: ConfigProperties;
  setProperties?: any;
}

export interface ValidationStateInterface {
  valid?: boolean;
  errorMessage?: string;
  errors?: ValidationError[];

  getErrorMessage(): string;
}

export interface ValidationError {
  key: string;
  field?: string;
  message: string;
}

export class ValidationState implements ValidationStateInterface {
  valid?: boolean;
  errorMessage?: string;
  errors?: ValidationError[];

  getErrorMessage(): string {
    if (this.errorMessage !== undefined) {
      return this.errorMessage;
    } else {
      return '';
    }
  }
}

export interface UserData {
  isAuthenticated: boolean;
  token?: string;
  userName?: string;
}

export interface AuthContextInterface {
  userData: UserData;
  updateContext?: any;
}

export interface RepoConfig {
  org?: string;
  repo?: string;
  branch?: string;
  buildSettings: BuildConfig;
  tests: Test[];
}

export interface BuildConfig {
  buildPromotes?: boolean;
  partOfOSRelease?: boolean;
  needsBase?: boolean;
  needsOS?: boolean;
  goVersion?: string;
  canonicalGoRepository?: string;
  baseImages: Image[];
  containerImages: ContainerImage[];
  buildCommands?: string;
  testBuildCommands?: string;
  operatorConfig: OperatorConfig;
  release: ReleaseConfig;
}

export interface OperatorConfig {
  isOperator: boolean;
  name?: string;
  dockerfilePath?: string;
  contextDir?: string;
  baseIndex?: string;
  updateGraph?: UpdateGraphType;
  substitutions: PullspecSubstitution[];
}

export interface ReleaseConfig {
  type: ReleaseType;
  version?: string;
}

export interface Image {
  name: string;
  namespace: string;
  tag: string;
}

export interface ContainerImage {
  name: string;
  from: string;
  literalDockerfile: boolean;
  dockerfile: string;
  inputs?: ContainerImageInput[];
}

export interface ContainerImageInput {
  name: string;
  replaces: string;
}

export interface PullspecSubstitution {
  pullspec: string;
  with: string;
}

export type Test = {
  name: string;
  requiresBuiltBinaries?: boolean;
  requiresTestBinaries?: boolean;
  testCommands?: string;
  from?: string;
  type: TestType;
  requiresCli: boolean;
  clusterProfile?: string;
  cloudProvider?: CloudProvider;
  operatorConfig?: OperatorTestConfig;
  env: { [env: string]: string };
  dependencies: { [env: string]: string };
};

export type OperatorTestConfig = {
  bundleName?: string;
  package?: string;
  channel?: string;
  installNamespace?: string;
  targetNamespaces?: string;
};

export enum TestType {
  Unit = 'Unit',
  E2e = 'E2e',
  Operator = 'Operator',
}

export enum CloudProvider {
  Aws = 'Aws',
  Azure = 'Azure',
  Gcp = 'Gcp',
}

export enum UpdateGraphType {
  semver = 'semver',
  semverSkippatch = 'semver_skippatch',
  replaces = 'release',
}

export enum ReleaseType {
  No = 'No',
  Published = 'Published',
  Nightly = 'Nightly',
}

export interface WizardStep {
  step?: number;
  stepIsComplete?: boolean;
  errorMessages?: string[];
  validator?: () => boolean;
}

export interface WizardContextInterface {
  step: WizardStep;
  setStep?: any;
}

export interface ConfigContextInterface {
  config: RepoConfig;
  setConfig?: any;
}

export const ConfigPropertiesContext = createContext({} as ConfigPropertiesContextInterface);
export const ConfigContext = createContext({} as ConfigContextInterface);
export const WizardContext = createContext({} as WizardContextInterface);
export const AuthContext = createContext({ userData: { isAuthenticated: false } } as AuthContextInterface);
