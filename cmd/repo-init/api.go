package main

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/ghodss/yaml"
	"github.com/sirupsen/logrus"

	prowConfig "k8s.io/test-infra/prow/config"
	"k8s.io/test-infra/prow/flagutil"
	"k8s.io/test-infra/prow/interrupts"
	"k8s.io/test-infra/prow/logrusutil"
	"k8s.io/test-infra/prow/metrics"
	"k8s.io/test-infra/prow/pjutil"
	"k8s.io/test-infra/prow/simplifypath"

	"github.com/openshift/ci-tools/pkg/api"
	"github.com/openshift/ci-tools/pkg/config"
	"github.com/openshift/ci-tools/pkg/secrets"
	"github.com/openshift/ci-tools/pkg/validation"
)

type server struct {
	// NOTE: this map should not be altered outside the loadServerConfig function.
	serverConfig map[serverConfigType]string

	githubOptions flagutil.GitHubOptions
	disableCors   bool
	rm            *repoManager

	logger *logrus.Entry
	censor *secrets.DynamicCensor
}

// l keeps the tree legible
func l(fragment string, children ...simplifypath.Node) simplifypath.Node {
	return simplifypath.L(fragment, children...)
}

type validationResponse struct {
	Valid            bool              `json:"valid"`
	Message          string            `json:"message"`
	ValidationErrors []validationError `json:"errors"`
}

type validationError struct {
	Key     string `json:"key"`
	Field   string `json:"field"`
	Message string `json:"message"`
}

type validationType string

const (
	All                  = validationType("ALL")
	BaseImages           = validationType("BASE_IMAGES")
	ContainerImages      = validationType("CONTAINER_IMAGES")
	Tests                = validationType("TESTS")
	OperatorBundle       = validationType("OPERATOR_BUNDLE")
	OperatorSubstitution = validationType("OPERATOR_SUBSTITUTION")

	GitHubClientId     = serverConfigType("github-client-id")
	GitHubClientSecret = serverConfigType("github-client-secret")
	GitHubRedirectUri  = serverConfigType("github-redirect-uri")
)

type serverConfigType string

var configTypes = []serverConfigType{GitHubClientId, GitHubClientSecret, GitHubRedirectUri}

func serveAPI(port, healthPort, numRepos int, ghOptions flagutil.GitHubOptions, disableCorsVerification bool, serverConfigPath string) {
	logrusutil.ComponentInit()
	// Set up a censor, so we don't log access tokens
	censor := secrets.NewDynamicCensor()
	logrus.SetFormatter(logrusutil.NewFormatterWithCensor(logrus.StandardLogger().Formatter, &censor))

	rm := &repoManager{
		numRepos: numRepos,
	}
	rm.init()

	s := server{
		logger:        logrus.WithField("component", "repo-init-apiserver"),
		githubOptions: ghOptions,
		disableCors:   disableCorsVerification,
		rm:            rm,
		censor:        &censor,
	}

	err := s.loadServerConfig(serverConfigPath)
	if err != nil {
		s.logger.WithError(err).Fatal("Unable to load server config")
	}

	health := pjutil.NewHealthOnPort(healthPort)
	health.ServeReady()

	metrics.ExposeMetrics("repo-init-api", prowConfig.PushGateway{}, flagutil.DefaultMetricsPort)
	simplifier := simplifypath.NewSimplifier(l("", // shadow element mimicking the root
		l("api",
			l("auth"),
			l("cluster-profiles"),
			l("configs"),
			l("config-validations"),
			l("server-configs"),
		),
	))

	apiMetrics := metrics.NewMetrics("repo_init_api")
	handler := metrics.TraceHandler(simplifier, apiMetrics.HTTPRequestDuration, apiMetrics.HTTPResponseSize)
	mux := http.NewServeMux()
	mux.HandleFunc("/api/auth", handler(s.authHandler()).ServeHTTP)
	mux.HandleFunc("/api/cluster-profiles", handler(s.clusterProfileHandler()).ServeHTTP)
	mux.HandleFunc("/api/configs", handler(s.configHandler()).ServeHTTP)
	mux.HandleFunc("/api/config-validations", handler(s.configValidationHandler()).ServeHTTP)
	mux.HandleFunc("/api/server-configs", handler(s.serverConfigHandler()).ServeHTTP)
	httpServer := &http.Server{Addr: ":" + strconv.Itoa(port), Handler: mux}
	interrupts.ListenAndServe(httpServer, 5*time.Second)
	s.logger.Debug("Ready to serve HTTP requests.")
}

func (s *server) loadServerConfig(configPath string) error {
	s.serverConfig = make(map[serverConfigType]string)
	fs, err := ioutil.ReadDir(configPath)
	if err != nil {
		return fmt.Errorf("error while loading server configs: %w", err)
	}

	for _, f := range fs {
		for _, configKey := range configTypes {
			if f.Name() == string(configKey) {
				filePath := filepath.Join(configPath, f.Name())

				fileContent, err := ioutil.ReadFile(filePath)
				if err != nil {
					return err
				}

				s.serverConfig[configKey] = strings.TrimSpace(string(fileContent))
				break
			}
		}
	}

	return nil
}

func (s *server) authHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		logger := s.logger.WithField("handler", "authHandler")
		s.disableCORS(w)
		if r.Method != http.MethodPost {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}

		code, err := ioutil.ReadAll(r.Body)
		if err != nil {
			logger.WithError(err).Error("unable to read request body")
			w.WriteHeader(http.StatusInternalServerError)
			return
		}

		data := url.Values{
			"client_id":     {s.serverConfig[GitHubClientId]},
			"client_secret": {s.serverConfig[GitHubClientSecret]},
			"code":          {string(code)},
			"redirect_uri":  {s.serverConfig[GitHubRedirectUri]},
		}

		// get the access token
		req, err := http.NewRequest("POST",
			"https://github.com/login/oauth/access_token",
			strings.NewReader(data.Encode()))
		if err != nil {
			logger.WithError(err).Error("unable to initialize request")
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.Header.Set("Accept", "application/json")

		client := &http.Client{}
		resp, err := client.Do(req)

		if err != nil {
			logger.WithError(err).Error("unable to get access token")
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		defer resp.Body.Close()

		var res map[string]string

		err = json.NewDecoder(resp.Body).Decode(&res)
		if err != nil {
			logger.WithError(err).Error("unable to decode response")
			w.WriteHeader(http.StatusInternalServerError)
			return
		}

		accessToken := res["access_token"]
		// We don't want to log this token
		s.censor.AddSecrets(accessToken)

		// get the user information
		ghClient := s.githubOptions.GitHubClientWithAccessToken(accessToken)
		user, err := ghClient.BotUser()
		if err != nil {
			logger.WithError(err).Error("unable to retrieve user")
			w.WriteHeader(http.StatusInternalServerError)
			return
		}

		w.WriteHeader(http.StatusOK)

		marshalled, err := json.Marshal(map[string]string{
			"accessToken": accessToken,
			"userName":    user.Login,
		})
		if err != nil {
			logger.WithError(err).Error("unable marshall data")
			w.WriteHeader(http.StatusInternalServerError)
			return
		}

		_, err = w.Write(marshalled)
		if err != nil {
			logger.WithError(err).Error("unable to write response")
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
	}
}

func (s *server) clusterProfileHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		logger := s.logger.WithField("handler", "clusterProfileHandler")
		s.disableCORS(w)
		switch r.Method {
		case http.MethodGet:
			marshalled, err := json.Marshal(clusterProfileList)
			if err != nil {
				w.WriteHeader(http.StatusInternalServerError)
				return
			}
			w.WriteHeader(http.StatusOK)
			_, err = w.Write(marshalled)
			if err != nil {
				w.WriteHeader(http.StatusInternalServerError)
				logger.WithError(err).Error("unable to marshall response")
				return
			}
		case http.MethodOptions:
			w.WriteHeader(http.StatusNoContent)
		default:
			w.WriteHeader(http.StatusMethodNotAllowed)
		}
	}
}

func (s *server) configValidationHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		s.disableCORS(w)
		switch r.Method {
		case http.MethodPost:
			s.validateConfig(w, r)
		case http.MethodOptions:
			w.WriteHeader(http.StatusNoContent)
		default:
			w.WriteHeader(http.StatusMethodNotAllowed)
		}
	}
}

func (s *server) configHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		s.disableCORS(w)
		switch r.Method {
		case http.MethodGet:
			s.loadConfigs(w, r)
		case http.MethodPost:
			s.generateConfig(w, r)
		case http.MethodOptions:
			w.WriteHeader(http.StatusNoContent)
		default:
			w.WriteHeader(http.StatusMethodNotAllowed)
		}
	}
}

func (s *server) serverConfigHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		logger := s.logger.WithField("handler", "serverConfigHandler")
		s.disableCORS(w)
		switch r.Method {
		case http.MethodGet:
			configMap := make(map[serverConfigType]string)

			for key, value := range s.serverConfig {
				if key != GitHubClientSecret {
					configMap[key] = value
				}
			}

			w.WriteHeader(http.StatusOK)
			marshalledConfig, err := json.Marshal(configMap)
			if err != nil {
				logger.WithError(err).Error("caught error marshalling configs")
				w.WriteHeader(http.StatusInternalServerError)
				return
			}
			_, _ = w.Write(marshalledConfig)
		case http.MethodOptions:
			w.WriteHeader(http.StatusNoContent)
		default:
			w.WriteHeader(http.StatusMethodNotAllowed)
		}
	}
}

func (s server) disableCORS(w http.ResponseWriter) {
	if s.disableCors {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "*")
		w.Header().Set("Access-Control-Allow-Headers", "*")
	}
}

func (s server) loadConfigs(w http.ResponseWriter, r *http.Request) {
	org := r.URL.Query().Get("org")
	repo := r.URL.Query().Get("repo")

	if org == "" {
		w.WriteHeader(http.StatusBadRequest)
		s.logger.Error("no org provided")
		_, _ = w.Write([]byte("You must provide an org when querying configs."))
		return
	}

	githubUser := r.Header.Get("github_user")
	availableRepo, err := s.rm.retrieveAndLockAvailable(githubUser)
	defer s.rm.returnInUse(availableRepo)
	if err != nil {
		w.WriteHeader(http.StatusBadRequest)
		s.logger.WithError(err).Error("unable to get available repo")
		_, _ = w.Write([]byte("Unable to retrieve a copy of the o/release repo to use. This probably just means that all of them are in use. Please try again in a few seconds."))
		return
	}
	releaseRepo := availableRepo.path

	//TODO(smg247): note, this generates an error inside that isn't really an error in this case. We need to make sure this is being filtered
	configs, err := config.LoadByOrgRepo(getConfigPath(org, repo, releaseRepo))

	if err != nil {
		s.logger.WithError(err).Error("Error while loading configs")
		w.WriteHeader(http.StatusBadRequest)
		return
	}

	if len(configs) == 0 {
		w.WriteHeader(http.StatusNotFound)
		return
	}

	marshalledConfigs, err := json.Marshal(configs)

	if err != nil {
		s.logger.WithError(err).Error("Error while marhalling configs")
		w.WriteHeader(http.StatusBadRequest)
		return
	}

	w.WriteHeader(http.StatusOK)
	_, err = w.Write(marshalledConfigs)
	if err != nil {
		s.logger.WithError(err).Error("Error while writing response")
		w.WriteHeader(http.StatusBadRequest)
		return
	}
}

type ValidationRequest struct {
	ValidationType validationType  `json:"validation_type"`
	Data           json.RawMessage `json:"data"`
}

type ConfigValidationRequest struct {
	Config initConfig `json:"config"`
}

type SubstitutionValidationRequest struct {
	ConfigValidationRequest
	Substitution api.PullSpecSubstitution `json:"substitution"`
}

func unmarshalValidationRequest(data []byte) (validationType, interface{}, error) {
	input := &ValidationRequest{}
	err := json.Unmarshal(data, input)
	if err != nil {
		return "", nil, err
	}

	switch input.ValidationType {
	case OperatorSubstitution:
		request := &SubstitutionValidationRequest{}
		err := json.Unmarshal(input.Data, request)

		return input.ValidationType, request, err
	default:
		request := &ConfigValidationRequest{}
		err := json.Unmarshal(input.Data, request)

		return input.ValidationType, request, err
	}
}

func (s server) validateConfig(w http.ResponseWriter, r *http.Request) {
	logger := s.logger.WithField("handler", "configValidationHandler")
	body, err := ioutil.ReadAll(r.Body)
	if err != nil {
		logger.WithError(err).Error("Error while reading request body")
		w.WriteHeader(http.StatusInternalServerError)
		return
	}

	validationType, validationObject, err := unmarshalValidationRequest(body)
	if err != nil {
		w.WriteHeader(http.StatusBadRequest)
		logger.WithError(err).Error("unable to unmarshal request")
		_, _ = w.Write([]byte("Invalid validation request"))
		return
	}
	logger.WithField("validationType", validationType).Debug("validating the config")

	var validationErrors []error

	// See if this is just acting on the whole configuration.
	if configRequest, ok := validationObject.(*ConfigValidationRequest); ok {
		dataWithInfo := generateCIOperatorConfig(configRequest.Config, nil)
		generated := &dataWithInfo.Configuration

		context := validation.NewConfigContext()

		switch validationType {
		case All:
			if err := validation.IsValidConfiguration(generated, configRequest.Config.Org, configRequest.Config.Repo); err != nil {
				validationErrors = append(validationErrors, err)
			}
		case BaseImages:
			validationErrors = append(validationErrors, validation.ValidateBaseImages(context.AddField("base_images"), generated.BaseImages)...)
		case ContainerImages:
			validation.ValidateImages(context.AddField("images"), generated.Images)
		case OperatorBundle:
			validationErrors = append(validationErrors, validation.ValidateOperator(context.AddField("operator_bundle"), generated)...)
		case Tests:
			// Build up a graph configuration with the relevant parts in order to validate the tests
			var rawSteps []api.StepConfiguration
			for _, t := range generated.Tests {
				test := t
				rawSteps = append(rawSteps, api.StepConfiguration{
					TestStepConfiguration: &test,
				})
			}
			for _, i := range generated.InputConfiguration.BaseImages {
				image := i
				rawSteps = append(rawSteps, api.StepConfiguration{
					InputImageTagStepConfiguration: &api.InputImageTagStepConfiguration{
						InputImage: api.InputImage{
							BaseImage: image,
							To:        api.PipelineImageStreamTagReference(image.Name),
						}}})
			}
			for _, i := range generated.Images {
				image := i
				rawSteps = append(rawSteps, api.StepConfiguration{
					ProjectDirectoryImageBuildStepConfiguration: &image,
				})
			}
			if err = validation.IsValidGraphConfiguration(rawSteps); err != nil {
				validationErrors = append(validationErrors, err)
			}
		default:
			logger.WithError(err).Error("Invalid validation type specified")
			w.WriteHeader(http.StatusBadRequest)
			return
		}
	} else if substitutionRequest, ok := validationObject.(*SubstitutionValidationRequest); ok {
		// We're validating an operator pullspec substitution
		dataWithInfo := generateCIOperatorConfig(substitutionRequest.Config, nil)
		generated := &dataWithInfo.Configuration

		linkForImage := func(image string) api.StepLink {
			return validation.LinkForImage(image, generated)
		}

		context := validation.NewConfigContext()
		if err := validation.ValidateOperatorSubstitution(context.AddField("operator_substitution"), substitutionRequest.Substitution, linkForImage); err != nil {
			validationErrors = append(validationErrors, err)
		}
	}

	response := validationResponse{Valid: true}
	if len(validationErrors) > 0 {
		response.Valid = false
		logger.WithError(err).Errorf("Caught errors %v", validationErrors)

		for _, e := range validationErrors {
			errorString := e.Error()
			errorComponents := strings.Split(errorString, ".")
			if len(errorComponents) > 1 {
				response.ValidationErrors = append(response.ValidationErrors, validationError{
					Field:   errorComponents[0],
					Message: errorComponents[len(errorComponents)-1],
				})
			} else {
				response.ValidationErrors = append(response.ValidationErrors, validationError{
					Key:     "generic",
					Message: errorString,
				})
			}
		}
		w.WriteHeader(http.StatusBadRequest)
	} else {
		w.WriteHeader(http.StatusOK)
	}
	marshalled, err := json.Marshal(response)
	if err != nil {
		logger.WithError(err).Error("Failed to marshal validation errors")
	}
	_, _ = w.Write(marshalled)
}

func getConfigPath(org, repo, releaseRepo string) string {
	pathElements := []string{releaseRepo, "ci-operator", "config", org}
	if repo != "" {
		pathElements = append(pathElements, repo)
	}
	configPath := path.Join(pathElements...)

	return configPath
}

// generateConfig is responsible for taking the initConfig and converting it into an api.ReleaseBuildConfiguration. Optionally
// this function may also push this config to GitHub and create a pull request for the o/release repo.
func (s server) generateConfig(w http.ResponseWriter, r *http.Request) {
	bodyBytes, err := ioutil.ReadAll(r.Body)
	if err != nil {
		w.WriteHeader(http.StatusBadRequest)
		s.logger.WithError(err).Error("Unable to read request body")
		return
	}

	var config initConfig
	s.logger.Debugf("Unmarshalled config as: %s", string(bodyBytes))
	err = json.Unmarshal(bodyBytes, &config)
	if err != nil {
		w.WriteHeader(http.StatusBadRequest)
		s.logger.WithError(err).Error("Unable to marshal request body")
		return
	}

	githubUser := r.Header.Get("github_user")
	// since we might be interacting with git, grab one of the checked out o/release repos and assign it to the current
	// user. we'll hold on to this until all git interactions are complete to prevent weirdness resulting from multiple users
	// dealing with the same working copy.
	repo, err := s.rm.retrieveAndLockAvailable(githubUser)
	if err != nil {
		w.WriteHeader(http.StatusBadRequest)
		s.logger.WithError(err).Error("unable to get available repo")
		_, _ = w.Write([]byte("Unable to retrieve a copy of the o/release repo to use. This probably just means that all of them are in use. Please try again in a few seconds."))
		return
	}

	releaseRepo := repo.path
	defer s.rm.returnInUse(repo)

	// if we're only converting the initConfig, then we won't commit any changes against the local working copy or create a pull request.
	if conversionOnly, err := strconv.ParseBool(r.URL.Query().Get("conversionOnly")); err == nil && conversionOnly {
		generatedConfig, err := createCIOperatorConfig(config, releaseRepo, false)

		if err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			s.logger.WithError(err).Error("could not generate new CI Operator configuration")
			return
		}
		marshalled, err := yaml.Marshal(generatedConfig)
		if err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			s.logger.WithError(err).Error("could not marshal CI Operator configuration")
			return
		}
		w.WriteHeader(http.StatusOK)

		if _, err := w.Write(marshalled); err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			s.logger.WithError(err).Error("Could not write CI Operator configuration response")
			return
		}
		return
	}

	exists := configExists(config.Org, config.Repo, releaseRepo)
	if exists {
		w.WriteHeader(http.StatusConflict)
		_, _ = fmt.Fprintf(w, "Config already exists for org: %s and repo: %s", config.Org, config.Repo)
		return
	}

	if err := updateProwConfig(config, releaseRepo); err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		s.logger.WithError(err).Error("could not update Prow configuration")
		return
	}

	if err := updatePluginConfig(config, releaseRepo); err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		s.logger.WithError(err).Error("could not update Prow plugin configuration")
		return
	}

	if _, err := createCIOperatorConfig(config, releaseRepo, true); err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		s.logger.WithError(err).Error("could not generate new CI Operator configuration")
		return
	}

	err = generateJobs(s.logger)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		s.logger.WithError(err).Error("failed to generate jobs")
		return
	}

	createPR, _ := strconv.ParseBool(r.URL.Query().Get("generatePR"))
	branch, err := pushChanges(repo, s.githubOptions, config.Org, config.Repo, githubUser, r.Header.Get("access_token"), createPR)

	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		s.logger.WithError(err).Error("could not push changes")
		return
	}

	w.WriteHeader(http.StatusOK)
	_, err = w.Write([]byte(fmt.Sprintf("https://github.com/%s/release/pull/new/%s", githubUser, branch)))

	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		s.logger.WithError(err).Error("error occurred while writing response")
		return
	}
}

func generateJobs(logger *logrus.Entry) error {
	logger.Debug("mimicking 'make jobs' prior to commit")
	steps := []struct {
		command   string
		arguments []string
	}{
		{
			command: "ci-operator-checkconfig",
			arguments: []string{
				"--config-dir", "./ci-operator/config",
				"--registry", "./ci-operator/step-registry",
			},
		},
		{
			command: "ci-operator-prowgen",
			arguments: []string{
				"--from-dir", "./ci-operator/config",
				"--to-dir", "./ci-operator/jobs",
			},
		},
		{
			command: "sanitize-prow-jobs",
			arguments: []string{
				"--prow-jobs-dir", "./ci-operator/jobs",
				"--config-path", "./core-services/sanitize-prow-jobs/_config.yaml",
			},
		},
	}
	for _, step := range steps {
		cmd := exec.Command(step.command, step.arguments...)
		if err := cmd.Run(); err != nil {
			return fmt.Errorf("error: %w while running: %s", err, step.command)
		}
	}

	logger.Debug("completed mimicking 'make jobs' prior to commit")
	return nil
}

func configExists(org, repo, releaseRepo string) bool {
	configPath := path.Join(releaseRepo, "ci-operator", "config", org, repo)
	_, err := os.Stat(configPath)
	return err == nil
}
