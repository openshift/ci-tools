import * as React from 'react';
import { ExclamationTriangleIcon } from '@patternfly/react-icons';
import {
  PageSection,
  Title,
  EmptyState,
  EmptyStateIcon,
  EmptyStateBody,
} from '@patternfly/react-core';
import { Link } from 'react-router-dom';

const NotFound: React.FunctionComponent = () => {
  return (
    <PageSection>
    <EmptyState variant="full">
      <EmptyStateIcon icon={ExclamationTriangleIcon} />
      <Title headingLevel="h1" size="lg">
        404 Page not found
      </Title>
      <EmptyStateBody>
        We didn&apos;t find a page that matches the address you navigated to.
      </EmptyStateBody>
      <Link to="/">Take me home</Link>
    </EmptyState>
  </PageSection>
  )
};

export { NotFound };
