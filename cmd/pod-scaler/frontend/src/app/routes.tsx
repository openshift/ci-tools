import * as React from 'react';
import { Route, RouteComponentProps, Switch } from 'react-router-dom';
import { accessibleRouteChangeHandler } from '@app/utils/utils';
import { Steps } from '@app/ResourceUsage/Steps/Steps';
import { NotFound } from '@app/NotFound/NotFound';
import { useDocumentTitle } from '@app/utils/useDocumentTitle';
import { LastLocationProvider, useLastLocation } from 'react-router-last-location';
import { Builds } from '@app/ResourceUsage/Builds/Builds';
import { ProwJobs } from '@app/ResourceUsage/ProwJobs/ProwJobs';
import { Pods } from '@app/ResourceUsage/Pods/Pods';
import { RPMs } from '@app/ResourceUsage/RPMRepos/RPMRepos';
import { Landing } from '@app/Landing/Landing';

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
    component: Landing,
    exact: true,
    label: 'About',
    path: '/',
    title: 'Resource Usage | About',
  },
  {
    label: 'Resource Usage',
    routes: [
      {
        component: Steps,
        exact: true,
        label: 'Steps',
        path: '/usage/steps',
        title: 'Resource Usage | Steps',
      },
      {
        component: Builds,
        exact: true,
        label: 'Builds',
        path: '/usage/builds',
        title: 'Resource Usage | Builds',
      },
      {
        component: Pods,
        exact: true,
        label: 'Pods',
        path: '/usage/pods',
        title: 'Resource Usage | Pods',
      },
      {
        component: ProwJobs,
        exact: true,
        label: 'ProwJobs',
        path: '/usage/prowjobs',
        title: 'Resource Usage | ProwJobs',
      },
      {
        component: RPMs,
        exact: true,
        label: 'RPM Repos',
        path: '/usage/rpms',
        title: 'Resource Usage | RPM Repos',
      },
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

  return <Route render={routeWithTitle} {...rest} />;
};

const PageNotFound = ({ title }: { title: string }) => {
  useDocumentTitle(title);
  return <Route component={NotFound} />;
};

const flattenedRoutes: IAppRoute[] = routes.reduce(
  (flattened, route) => [...flattened, ...(route.routes ? route.routes : [route])],
  [] as IAppRoute[]
);

const AppRoutes = (): React.ReactElement => (
  <LastLocationProvider>
    <Switch>
      {flattenedRoutes.map(({ path, exact, component, title, isAsync }, idx) => (
        <RouteWithTitleUpdates
          path={path}
          exact={exact}
          component={component}
          key={idx}
          title={title}
          isAsync={isAsync}
        />
      ))}
      <PageNotFound title="404 Page Not Found" />
    </Switch>
  </LastLocationProvider>
);

export { AppRoutes, routes };
