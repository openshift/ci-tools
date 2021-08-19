import * as React from 'react';
import {
    PageSection,
} from '@patternfly/react-core';
import {Resources} from "@app/Resources/Resources";

const ProwJobs: React.FunctionComponent = () => {
    return <PageSection>
        <Resources urlFragment={"prowjobs"} workload={"ProwJob"}/>
    </PageSection>
}

export { ProwJobs };
