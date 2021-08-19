import {Buffer} from 'buffer';
import * as React from 'react';
import {Alert, Flex, Spinner} from '@patternfly/react-core';
import {DeserializeHistogram, Histogram} from "@app/CircLLHist/CircLLHist";
import {LogarithmicComparativePlot} from "@app/CircLLHist/LogarithmicComparativePlot";

export interface HistogramsProps {
    /** URL to fetch raw data from */
    dataUrl: string;
    /** Query parameters for the fetch */
    parameters: string;
}

export interface rawData {
    cutoff: string;
    lower_bound: string;
    merged: string;
    histograms: string[];
}

export interface Data {
    cutoff: number;
    lower_bound: number;
    merged: Histogram;
    histograms: Histogram[];
}

export type HistogramData = Record<string, Data>;

const process = (raw: Record<string, rawData>): HistogramData => {
    const data: HistogramData = {};
    for (const resource in raw) {
            const datum: Data = {
                cutoff: parseFloat(raw[resource].cutoff),
                lower_bound: parseFloat(raw[resource].lower_bound),
                merged: DeserializeHistogram(Buffer.from(raw[resource].merged, 'base64')),
                histograms: [],
            };
            for (const histogram of raw[resource].histograms) {
                datum.histograms.push(DeserializeHistogram(Buffer.from(histogram, 'base64')))
            }
            data[resource] = datum;
    }
    return data;
}

export const Histograms: React.FunctionComponent<HistogramsProps> = (
    {
        dataUrl,
        parameters,
    }: HistogramsProps) => {
    const [data, setData] = React.useState<HistogramData>({});
    const [fetchError, setFetchError] = React.useState<string>("");

    React.useEffect(() => {
        let mounted = true;
        fetch(dataUrl + "?" + new URLSearchParams(parameters), {headers: {"Accept": "application/json"}}).then(async (res) => {
            if (!res.ok) {
                const raw = await res.text();
                throw new Error(res.status + ": " + raw);
            }
            const raw = await res.json();
            if (mounted) {
                const processed = process(raw);
                setData(processed);
            }
        }).catch((error) => {
            if (mounted) {
                setFetchError(String(error));
            }
        })
        return () => {
            mounted = false
        };
    }, [dataUrl, parameters]);

    if (fetchError) {
        return <div><Alert variant="danger" title={fetchError}/></div>
    }

    if (!data) {
        return <div><Spinner isSVG size="xl"/>Loading resource usage data...</div>
    }

    return <Flex direction={{default: 'row'}}
                 flexWrap={{default: 'wrap', lg: "nowrap", xl: "nowrap", '2xl': "nowrap"}}
                 justifyContent={{default: 'justifyContentSpaceAround'}}
                 alignItems={{default: 'alignItemsCenter'}}
                 alignContent={{default: 'alignContentStretch'}}>
        {data["cpu"] && <LogarithmicComparativePlot
            {...data["cpu"]}
            canvasProps={{
                title: "CPU Usage",
                yAxisFormatter(value: number): string {
                    const n: number = value * 1000;
                    if (value > 10) {
                        Math.round(n).toString();
                    }
                    return n.toFixed(2);
                },
                yAxisMin: 1e-5,
                yAxisTitle: "CPU Used",
                yAxisUnit: "mCPU",
            }}/>}
        {data["memory"] && <LogarithmicComparativePlot
            {...data["memory"]}
            canvasProps={{
                title: "Memory Usage",
                yAxisFormatter(value: number): string {
                    const n: number = value / Math.pow(2, 20);
                    if (value > 10) {
                        Math.round(n).toString();
                    }
                    return n.toFixed(2);
                },
                yAxisMin: 10 * Math.pow(2, 20),
                yAxisTitle: "Memory Used",
                yAxisUnit: "MiB",
            }}/>}
    </Flex>;
};

Histograms.displayName = 'Histograms';
