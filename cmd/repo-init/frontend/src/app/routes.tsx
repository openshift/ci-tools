import * as React from 'react';
import { Route, RouteComponentProps, Switch } from 'react-router-dom';
import { accessibleRouteChangeHandler } from '@app/utils/utils';
import { NotFound } from '@app/NotFound/NotFound';
import { useDocumentTitle } from '@app/utils/useDocumentTitle';
import { LastLocationProvider, useLastLocation } from 'react-router-last-location';
import RepoInitWizard from "@app/RepoInitWizard/RepoInitWizard";
import {ConfigEditor} from "@app/ConfigEditor/ConfigEditor";
import {Login} from "@app/Login/Login";
import {Home} from "@app/Home/Home";

let routeFocusTimer: number;
export interface IAppRoute {
  label?: string; // Excluding the label will exclude the route from the nav sidebar in AppLayout
  /* eslint-disable @typescript-eslint/no-explicit-any */
  component: React.ComponentType<RouteComponentProps<any>> | React.ComponentType<any>;
  /* eslint-enable @typescript-eslint/no-explicit-any */
  exact?: boolean;
  path: string;
  title: string;
  isAsync?: boolean;
  routes?: undefined;
}

export interface IAppRouteGroup {
  label: string;
  routes: IAppRoute[];
}

export type AppRouteConfig = IAppRoute | IAppRouteGroup;

const routes: AppRouteConfig[] = [
  {
    label: 'Repo Configuration',
    routes: [
      {
        component: Home,
        exact: true,
        label: 'Home',
        path: '/',
        title: 'Home',
      },
      {
        component: RepoInitWizard,
        exact: true,
        label: 'Add New Repo',
        path: '/repo-init',
        title: 'Add New Repo',
      },
      {
        component: Login,
        exact: true,
        path: '/login',
        title: 'Login'
      },
      {
        component: ConfigEditor,
        path: '/config-editor',
        title: 'Edit Raw Config',
      }
    ],
  },
];

// a custom hook for sending focus to the primary content container
// after a view has loaded so that subsequent press of tab key
// sends focus directly to relevant content
const useA11yRouteChange = (isAsync: boolean) => {
  const lastNavigation = useLastLocation();
  React.useEffect(() => {
    if (!isAsync && lastNavigation !== null) {
      routeFocusTimer = accessibleRouteChangeHandler();
    }
    return () => {
      window.clearTimeout(routeFocusTimer);
    };
  }, [isAsync, lastNavigation]);
};

const RouteWithTitleUpdates = ({ component: Component, isAsync = false, title, ...rest }: IAppRoute) => {
  useA11yRouteChange(isAsync);
  useDocumentTitle(title);

  function routeWithTitle(routeProps: RouteComponentProps) {
    return <Component {...rest} {...routeProps} />;
  }

  return <Route render={routeWithTitle} {...rest}/>;
};

const PageNotFound = ({ title }: { title: string }) => {
  useDocumentTitle(title);
  return <Route component={NotFound} />;
};

const flattenedRoutes: IAppRoute[] = routes.reduce(
  (flattened, route) => [...flattened, ...(route.routes ? route.routes : [route])],
  [] as IAppRoute[]
);


const AppRoutes = (): React.ReactElement => {
    return <LastLocationProvider>
      <Switch>
        {flattenedRoutes.map(({path, exact, component, title, isAsync}, idx) => (
          <RouteWithTitleUpdates
            path={path}
            exact={exact}
            component={component}
            key={idx}
            title={title}
            isAsync={isAsync}
          />
        ))}
        <PageNotFound title="404 Page Not Found"/>
      </Switch>
    </LastLocationProvider>

};

export { AppRoutes, routes };
