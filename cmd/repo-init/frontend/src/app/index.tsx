import * as React from 'react';
import {useState} from 'react';
import '@patternfly/react-core/dist/styles/base.css';
import {BrowserRouter as Router} from 'react-router-dom';
import {AppLayout} from '@app/AppLayout/AppLayout';
import {AppRoutes} from '@app/routes';
import {AuthContext, ConfigContext, ghAuthState, ReleaseType, RepoConfig} from '@app/types'
import '@app/app.css';

const App: React.FunctionComponent = () => {
  const [auth, setAuth] = useState({
    isAuthenticated: ghAuthState.isAuthenticated,
    userData: {token: ghAuthState.token}
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
        type: ReleaseType.No
      },
      operatorConfig: {
        isOperator: false,
        substitutions: []
      }
    }
  } as RepoConfig)

  return <AuthContext.Provider value={{userData: auth, updateContext: setAuth}}>
    <Router>
      <AppLayout>
        <ConfigContext.Provider value={{config: config, setConfig: setConfig}}>
          <AppRoutes/>
        </ConfigContext.Provider>
      </AppLayout>
    </Router>
  </AuthContext.Provider>
};

export default App;
