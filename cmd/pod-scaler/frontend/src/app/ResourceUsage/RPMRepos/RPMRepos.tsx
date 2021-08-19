import * as React from 'react';
import {
    PageSection,
} from '@patternfly/react-core';
import {Resources} from "@app/Resources/Resources";

const RPMs: React.FunctionComponent = () => {
    return <PageSection>
        <Resources urlFragment={"rpms"} workload={"RPM Repo"}/>
    </PageSection>
}

export { RPMs };
