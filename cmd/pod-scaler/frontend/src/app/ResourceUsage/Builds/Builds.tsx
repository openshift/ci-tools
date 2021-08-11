import * as React from 'react';
import {
    PageSection,
} from '@patternfly/react-core';
import {Resources} from "@app/Resources/Resources";

const Builds: React.FunctionComponent = () => {
    return <PageSection>
        <Resources urlFragment={"builds"} workload={"Build"}/>
    </PageSection>
}

export { Builds };
