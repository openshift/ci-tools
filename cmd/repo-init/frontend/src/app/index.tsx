import * as React from 'react';
import { useEffect, useState } from 'react';
import '@patternfly/react-core/dist/styles/base.css';
import { BrowserRouter as Router } from 'react-router-dom';
import { AppLayout } from '@app/AppLayout/AppLayout';
import { AppRoutes } from '@app/routes';
import { AuthContext, ConfigContext, ConfigPropertiesContext, ghAuthState, ReleaseType, RepoConfig } from '@app/types';
import '@app/app.css';

const App: React.FunctionComponent = () => {
  const [auth, setAuth] = useState({
    isAuthenticated: ghAuthState.isAuthenticated,
    userData: { token: ghAuthState.token },
  });
  const [configProperties, setConfigProperties] = useState({
    githubApiUrl: undefined,
    githubClientId: undefined,
    githubRedirectUrl: undefined,
    loaded: false,
  });
  const [config, setConfig] = useState({
    branch: 'master',
    tests: [],
    e2eTests: [],
    buildSettings: {
      baseImages: [],
      containerImages: [],
      goVersion: '1.17',
      release: {
        type: ReleaseType.No,
      },
      operatorConfig: {
        isOperator: false,
        substitutions: [],
      },
    },
  } as RepoConfig);

  useEffect(() => {
    if (!configProperties.loaded) {
      fetch(process.env.REACT_APP_API_URI + '/server-configs', {
        method: 'GET',
        headers: {
          'Content-Type': 'application/json',
        },
      })
        .then((r) => {
          return r.json().then((properties) => {
            const ghProperties = {
              githubApiUrl: properties['github-endpoint'],
              githubClientId: properties['github-client-id'],
              githubRedirectUrl: properties['github-redirect-uri'],
              loaded: true,
            };
            setConfigProperties(ghProperties);
          });
        })
        .catch(() => {
          return undefined;
        });
    }
  }, [configProperties]);

  return (
    <ConfigPropertiesContext.Provider
      value={{ configProperties: configProperties, setProperties: setConfigProperties }}
    >
      <AuthContext.Provider value={{ userData: auth, updateContext: setAuth }}>
        <Router>
          <AppLayout>
            <ConfigContext.Provider value={{ config: config, setConfig: setConfig }}>
              <AppRoutes />
            </ConfigContext.Provider>
          </AppLayout>
        </Router>
      </AuthContext.Provider>
    </ConfigPropertiesContext.Provider>
  );
};

export default App;
