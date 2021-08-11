import * as React from 'react';
import {
    PageSection,
} from '@patternfly/react-core';
import {Resources} from "@app/Resources/Resources";

const Steps: React.FunctionComponent = () => {
    return <PageSection>
        <Resources urlFragment={"steps"} workload={"Step"}/>
    </PageSection>
}

export { Steps };
