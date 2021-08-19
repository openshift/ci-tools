import * as React from 'react';
import {Flex, FlexItem, PageSection, Text, TextContent, TextVariants,} from '@patternfly/react-core';

const Landing: React.FunctionComponent = () => {
  return <PageSection>
    <Flex direction={{default: 'column'}}
          justifyContent={{default: 'justifyContentSpaceAround'}}
          alignContent={{default: 'alignContentCenter'}}>
      <FlexItem>
        <TextContent>
          <Text component={TextVariants.h1}>How To Read These Charts</Text>
          <Text component={TextVariants.p}>These charts are histogram heatmaps, presenting distributions of resource
            usage for all executions of the CI container that have been indexed. Each vertical slice is a histogram, so
            a block represents the amount of time (number of samples) that the specific execution of the CI container
            spent using that much of the resource. Colors represent relative density - the yellower a block, the higher
            the corresponding bar in the histogram would be. The left-most vertical slice is the aggregate distribution,
            which contains all of the data presented and is used to calculate the resource request recommendation. Note
            that the histograms used for storing distributions use an adaptive bucket size which varies with the
            logarithm of the values stored. As a result, the Y axis in the heatmaps are logarithmic, not linear, or
            smaller buckets would be almost invisible.</Text>
          <Text component={TextVariants.h1}>How Data is Stored</Text>
          <Text component={TextVariants.p}>In order to provide an estimate of resource usage for containers in a CI job,
            this server analyzes metrics from previous executions of similar containers. Aggregate statistics are used
            to provide resource request recommendations by digesting prior metrics. It is assumed that, for a
            sufficiently similar container, resource usage will not vary much across executions - we expect this to be
            true for <i>e.g.</i> all executions of unit tests for some branch on a repository. This assumption allows
            for samples from all executions to be treated as one dataset with a single underlying distribution, so that
            aggregation can be done on the larger dataset to yield higher-fidelity signal. The overall size of the raw
            data, however, quickly grows unmanageable. In order to operate efficiently on this dataset we therefore
            store compressed histograms for each execution trace. This allows us to reduce the data footprint while
            continuing to allow for dataset merging and aggregation. The <a
              href="https://www.circonus.com/2018/11/the-problem-with-percentiles-aggregation-brings-aggravation/">Circonus
              log-linear histogram</a> is used as it&lsquo;s performant, accurate, efficient and open-source.</Text>
        </TextContent>
      </FlexItem>
    </Flex>
  </PageSection>;
}

export {Landing};
