import * as React from 'react';
import {
    PageSection,
} from '@patternfly/react-core';
import {Resources} from "@app/Resources/Resources";

const Pods: React.FunctionComponent = () => {
    return <PageSection>
        <Resources urlFragment={"pods"} workload={"Pod"}/>
    </PageSection>
}

export { Pods };
