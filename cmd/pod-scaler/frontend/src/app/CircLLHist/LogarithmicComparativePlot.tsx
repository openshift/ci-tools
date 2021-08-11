import * as React from 'react';
import {Flex, FlexItem, Slider, Text, TextContent, TextVariants} from '@patternfly/react-core';
import {Bin, Histogram, Value, ValueBounds, Width} from "@app/CircLLHist/CircLLHist";
import * as d3 from 'd3-scale-chromatic';

export interface LogarithmicComparativePlotProps {
  /** the cutoff to display on the plot */
  cutoff: number;
  /** the lower bound for display on the plot */
  lower_bound: number;
  /** the merged data to show as a summary on the left of the plot */
  merged: Histogram;
  /** the data to plot */
  histograms: Histogram[];
  /** options for the canvas layout */
  canvasProps: CanvasProps & OptionalCanvasProps;
}

export interface CanvasProps {
  /** title of the plot */
  title: string;
  /** title of the Y axis */
  yAxisTitle: string;
  /** units on the Y axis */
  yAxisUnit: string;
  /** minimum value for the Y axis */
  yAxisMin: number;
  /** formatter for Y axis ticks */
  yAxisFormatter: (value: number) => string;
}

export interface OptionalCanvasProps {
  /** horizontal offset for the plot, in pixels */
  sideOffset?: number;
  /** vertical offset for the plot, in pixels */
  verticalOffset?: number;
  /** extra offset for the top of the plot, in pixels */
  topOffset?: number;
  /** width of a cell, in pixels */
  cellWidth?: number;
  /** height of the data in the plot, in pixels */
  dataHeight?: number;
}

const defaultSideOffset = 100;
const defaultVerticalOffset = 20;
const defaultTopOffset = 50;
const defaultCellWidth = 20;
const defaultDataHeight = 1000;


type CanvasMetadata = CanvasProps & ProvidedMetadata & ComputedMetadata;

interface ProvidedMetadata {
  /** horizontal offset for the plot, in pixels */
  sideOffset: number;
  /** vertical offset for the plot, in pixels */
  verticalOffset: number;
  /** extra offset for the top of the plot, in pixels */
  topOffset: number;
  /** width of a cell, in pixels */
  cellWidth: number;
  /** height of the data in the plot, in pixels */
  dataHeight: number;
}

interface ComputedMetadata {
  /** the number of columns in the plot */
  numCols: number;
  /** the width of the plot, in pixels */
  width: number;
  /** the height of the plot, in pixels */
  height: number;

  /** the most negative value in the plot */
  minimum: number;
  /** the most positive value in the plot */
  maximum: number;

  /** state stored for ID generation */
  nextId: number;
}

const canvasMetaFromProps = (props: LogarithmicComparativePlotProps, windowWidth?: number): CanvasMetadata => {
  const canvasProps: OptionalCanvasProps = props.canvasProps as OptionalCanvasProps ? props.canvasProps as OptionalCanvasProps : {};
  const canvasMeta: CanvasMetadata = {
    ...props.canvasProps as CanvasProps,

    sideOffset: canvasProps.sideOffset ? canvasProps.sideOffset : defaultSideOffset,
    verticalOffset: canvasProps.verticalOffset ? canvasProps.verticalOffset : defaultVerticalOffset,
    topOffset: canvasProps.topOffset ? canvasProps.topOffset : defaultTopOffset,
    cellWidth: canvasProps.cellWidth ? canvasProps.cellWidth : defaultCellWidth,
    dataHeight: canvasProps.dataHeight ? canvasProps.dataHeight : defaultDataHeight,

    height: 0, numCols: 0, width: 0, minimum: 0, maximum: 0, nextId: 1,
  };

  const width = windowWidth ? windowWidth : 11 * canvasMeta.cellWidth + 2 * canvasMeta.sideOffset;
  const maxColumns = Math.floor((width - canvasMeta.sideOffset * 2) / canvasMeta.cellWidth);
  if (props.histograms.length + 1 > maxColumns) {
    canvasMeta.numCols = maxColumns
  } else {
    canvasMeta.numCols = props.histograms.length + 1;
  }
  canvasMeta.width = canvasMeta.sideOffset * 2 + canvasMeta.numCols * canvasMeta.cellWidth;
  canvasMeta.height = canvasMeta.dataHeight + 2 * canvasMeta.verticalOffset + canvasMeta.topOffset;

  /* eslint-disable-next-line prefer-const */
  let [minimum, maximum] = ValueBounds(props.merged);
  if (minimum < props.canvasProps.yAxisMin) {
    // in any case, let the user override it
    minimum = props.canvasProps.yAxisMin;
  }
  if (minimum < props.lower_bound) {
    // we can get away with not showing a long tail
    minimum = props.lower_bound;
  }
  canvasMeta.minimum = minimum;
  canvasMeta.maximum = maximum;

  return canvasMeta;
}

/** determines the Y coordinate in pixels for the value in the plot */
const pixelCoordinate = (canvasMeta: CanvasMetadata, value: number): number => {
  const fraction: number = (
    (Math.log10(value) - Math.log10(canvasMeta.minimum)) /
    (Math.log10(canvasMeta.maximum) - Math.log10(canvasMeta.minimum))
  )
  return canvasMeta.topOffset + canvasMeta.verticalOffset + canvasMeta.dataHeight - Math.round(fraction * canvasMeta.dataHeight);
}

/** generate a color for the next identifier see: http://stackoverflow.com/a/15804183 */
const nextColor = (canvasMeta: CanvasMetadata): string => {
  const colors: number[] = [];
  if (canvasMeta.nextId < 16777215) {
    colors.push(canvasMeta.nextId & 0xff); // R
    colors.push((canvasMeta.nextId & 0xff00) >> 8); // G
    colors.push((canvasMeta.nextId & 0xff0000) >> 16); // B
    canvasMeta.nextId += 1;
  }
  return "rgb(" + colors.join(',') + ")";
}

function draw(canvasMeta: CanvasMetadata, cutoff: number, merged: Histogram, histograms: Histogram[], mainCanvas: HTMLCanvasElement, hiddenCanvas: HTMLCanvasElement): Record<string, string> {
  const index: Record<string, string> = {};
  const mainContext = mainCanvas.getContext('2d');
  const hiddenContext = hiddenCanvas.getContext('2d');
  if (!mainContext || !hiddenContext) {
    return index
  }
  mainContext.clearRect(0, 0, canvasMeta.width, canvasMeta.height);
  hiddenContext.clearRect(0, 0, canvasMeta.width, canvasMeta.height);
  drawAxes(mainContext, canvasMeta, cutoff);
  addBucketsTo(merged.bins, index, mainContext, hiddenContext, canvasMeta.sideOffset, canvasMeta);
  outlineMerged(mainContext, canvasMeta);

  for (let i = 0; i < histograms.length; i++) {
    const offset = canvasMeta.sideOffset + (i + 1) * canvasMeta.cellWidth + 1;
    addBucketsTo(histograms[i].bins, index, mainContext, hiddenContext, offset, canvasMeta)
  }
  return index;
}

function outlineMerged(context: CanvasRenderingContext2D, canvasMeta: CanvasMetadata) {
  context.lineWidth = 1;
  context.strokeStyle = "black";
  context.beginPath();
  context.moveTo(canvasMeta.sideOffset - 1, canvasMeta.topOffset + canvasMeta.verticalOffset - 1);
  context.lineTo(canvasMeta.sideOffset + canvasMeta.cellWidth + 1, canvasMeta.topOffset + canvasMeta.verticalOffset - 1);
  context.lineTo(canvasMeta.sideOffset + canvasMeta.cellWidth + 1, canvasMeta.topOffset + canvasMeta.verticalOffset + canvasMeta.dataHeight + 1);
  context.lineTo(canvasMeta.sideOffset - 1, canvasMeta.topOffset + canvasMeta.verticalOffset + canvasMeta.dataHeight + 1);
  context.lineTo(canvasMeta.sideOffset - 1, canvasMeta.topOffset + canvasMeta.verticalOffset - 1);
  context.stroke();
}

type sign = -1 | 1;
type side = "left" | "right";

function drawAxes(context: CanvasRenderingContext2D, canvasMeta: CanvasMetadata, cutoff: number) {
  context.textAlign = "center";
  context.textBaseline = "top";
  context.font = "20pt RedHatFont";
  context.fillStyle = "black";
  context.fillText(canvasMeta.yAxisTitle, canvasMeta.width / 2, 10)
  drawAxis(context, canvasMeta, canvasMeta.sideOffset, -1, "right", cutoff)
  drawAxis(context, canvasMeta, canvasMeta.width - canvasMeta.sideOffset, 1, "left", cutoff)
}

function drawAxis(context: CanvasRenderingContext2D, canvasMeta: CanvasMetadata, axisXBase: number, sign: sign, side: side, cutoff: number) {
  const axisX = axisXBase + sign * 10;
  const axisYTop = canvasMeta.verticalOffset + canvasMeta.topOffset,
    axisYBottom = canvasMeta.verticalOffset + canvasMeta.topOffset + canvasMeta.dataHeight;
  context.font = "14pt Verdana";
  context.textAlign = side;
  context.textBaseline = "alphabetic";
  context.fillStyle = "black";
  context.fillText(canvasMeta.yAxisUnit, axisX, axisYTop - 15)
  lineBetween(context, 2, "black", {x: axisX, y: axisYTop}, {x: axisX, y: axisYBottom})
  const majorTicks: number[] = [];
  const numMajorTicks = 5;
  const numMinorTicks = 10;
  for (let i = 0; i < numMajorTicks + 1; i++) {
    majorTicks.push(Math.pow(10, Math.log10(canvasMeta.minimum) + i * (Math.log10(canvasMeta.maximum) - Math.log10(canvasMeta.minimum)) / numMajorTicks))
  }
  majorTick(context, axisX, majorTicks[0], "black", canvasMeta, sign, side)
  for (let i = 1; i < majorTicks.length; i++) {
    let previous = majorTicks[i - 1];
    let distance = (majorTicks[i] - majorTicks[i - 1]) / 3;
    for (let j = 0; j < numMinorTicks; j++) {
      const tick = previous + distance;
      minorTick(context, axisX, tick, canvasMeta, sign)
      distance = (majorTicks[i] - tick) / 3;
      previous = tick;
    }
    majorTick(context, axisX, majorTicks[i], "black", canvasMeta, sign, side)
  }
  majorTick(context, axisX, cutoff, "red", canvasMeta, sign, side)
}

function majorTick(context: CanvasRenderingContext2D, axisX: number, value: number, style: string, canvasMeta: CanvasMetadata, sign: sign, side: side) {
  const tickY = pixelCoordinate(canvasMeta, value);
  lineBetween(context, 2, style, {x: axisX + sign * 10, y: tickY}, {x: axisX, y: tickY});
  context.font = "10pt Verdana";
  context.textAlign = side;
  context.textBaseline = "middle";
  context.fillStyle = style;
  context.fillText(canvasMeta.yAxisFormatter(value), axisX + sign * 11, tickY)
}

function minorTick(context: CanvasRenderingContext2D, axisX: number, value: number, canvasMeta: CanvasMetadata, sign: sign) {
  const tickY = pixelCoordinate(canvasMeta, value);
  lineBetween(context, 1, "black", {x: axisX + sign * 5, y: tickY}, {x: axisX, y: tickY});
}

interface point {
  x: number;
  y: number;
}

function lineBetween(context: CanvasRenderingContext2D, width: number, style: string, a: point, b: point) {
  context.lineWidth = width;
  context.strokeStyle = style;
  context.beginPath();
  context.moveTo(a.x, a.y);
  context.lineTo(b.x, b.y);
  context.stroke();
}

function addBucketsTo(bins: Bin[], index: Record<string, string>, mainContext: CanvasRenderingContext2D, hiddenContext: CanvasRenderingContext2D, offset: number, canvasMeta: CanvasMetadata) {
  let largest = 0;
  for (const bin of bins) {
    if (bin.count > largest) {
      largest = bin.count
    }
  }
  mainContext.fillStyle = d3.interpolateCividis(0);
  mainContext.globalCompositeOperation = "destination-over";
  mainContext.fillRect(offset, canvasMeta.verticalOffset + canvasMeta.topOffset, canvasMeta.cellWidth, canvasMeta.dataHeight)
  mainContext.globalCompositeOperation = "source-over";
  for (const bin of bins) {
    if (bin.count === 0) {
      continue
    }
    const bottom = Value(bin);
    const height = Width(bin);
    const top: number = bottom + height;
    if (bottom === 0 || height === 0 || bottom < canvasMeta.minimum || top < canvasMeta.minimum) {
      continue
    }
    mainContext.fillStyle = d3.interpolateCividis(bin.count / largest)
    const topY = pixelCoordinate(canvasMeta, top);
    const bottomY = pixelCoordinate(canvasMeta, bottom);
    const heightY = bottomY - topY;
    mainContext.fillRect(offset, topY, canvasMeta.cellWidth, heightY)

    const hiddenColor = nextColor(canvasMeta);
    index[hiddenColor] = "(" + canvasMeta.yAxisFormatter(bottom) + "," + canvasMeta.yAxisFormatter(bottom + height) + ")" + canvasMeta.yAxisUnit + ": " + bin.count + " samples";
    hiddenContext.fillStyle = hiddenColor
    hiddenContext.fillRect(offset, topY, canvasMeta.cellWidth, heightY)
  }
}

export const LogarithmicComparativePlot: React.FunctionComponent<LogarithmicComparativePlotProps> = (props: LogarithmicComparativePlotProps) => {
  const container: React.RefObject<HTMLDivElement> = React.useRef<HTMLDivElement>(null);
  const [canvasMeta, setCanvasMeta] = React.useState<CanvasMetadata>(canvasMetaFromProps(props, undefined));

  // re-compute window on resize
  React.useEffect(() => {
    const handleResize = () => {
      if (container.current) {
        const width = container.current.getBoundingClientRect().width;
        setCanvasMeta(canvasMetaFromProps(props, width))
      }
    };
    window.addEventListener('resize', handleResize);
    handleResize();
    return () => {
      window.removeEventListener('resize', handleResize);
    };
  }, [container, props]);

  const [first, setFirst] = React.useState<number>(0);

  const onChange = (value: number): void => {
    setFirst(value);
  };

  const [index, setIndex] = React.useState<Record<string, string>>({});
  const mainCanvas: React.RefObject<HTMLCanvasElement> = React.useRef<HTMLCanvasElement>(null);
  const hiddenCanvas: React.RefObject<HTMLCanvasElement> = React.useRef<HTMLCanvasElement>(null);
  const tooltip: React.RefObject<HTMLDivElement> = React.useRef<HTMLDivElement>(null);

  // recompute meta when data changes
  React.useEffect(() => {
    if (container.current) {
      const width = container.current.getBoundingClientRect().width;
      setCanvasMeta(canvasMetaFromProps(props, width))
    }
  }, [props])

  // draw the canvases whenever we change the data or bounds
  React.useEffect(() => {
    if (mainCanvas.current && hiddenCanvas.current) {
      setIndex(draw(canvasMeta, props.cutoff, props.merged, props.histograms.slice(first, first + canvasMeta.numCols - 1), mainCanvas.current, hiddenCanvas.current));
    }
  }, [canvasMeta, props.cutoff, props.merged, props.histograms, first])

  React.useEffect(() => {
    const handleMouseMove = (event: MouseEvent): void => {
      if (mainCanvas.current && hiddenCanvas.current && tooltip.current) {
        const context = hiddenCanvas.current.getContext("2d");
        if (!context) {
          return;
        }
        const color = context.getImageData(event.offsetX, event.offsetY, 1, 1).data;
        const key = "rgb(" + color[0] + "," + color[1] + "," + color[2] + ")";
        const rect = mainCanvas.current.getBoundingClientRect();
        if (key in index) {
          tooltip.current.style.opacity = "0.8";
          tooltip.current.style.top = rect.y + event.offsetY + 5 + "px";
          tooltip.current.style.left = rect.x + event.offsetX + 5 + "px";
          tooltip.current.innerHTML = index[key];
        } else {
          tooltip.current.style.opacity = "0";
        }
      }
    };
    if (mainCanvas.current) {
      const node = mainCanvas.current;
      node.addEventListener('mousemove', handleMouseMove);
      return () => {
        node.removeEventListener('mousemove', handleMouseMove);
      };
    }
    return;
  }, [index, hiddenCanvas, mainCanvas, tooltip]);

  const request: string = canvasMeta.yAxisFormatter(props.cutoff);
  let samples = 0;
  for (const bin of props.merged.bins) {
    samples += bin.count;
  }
  const description: string = "Analyzing " + samples + " samples over " + props.histograms.length + " traces, a request of " + request + canvasMeta.yAxisUnit + " is recommended.";

  let sliderMax: number = props.histograms.length - canvasMeta.numCols + 1;
  if (sliderMax <= 0) {
    sliderMax = 0;
  }

  return <Flex direction={{default: 'column'}}
               justifyContent={{default: 'justifyContentSpaceAround'}}
               alignContent={{default: 'alignContentCenter'}}
               grow={{default: "grow"}}>
    <FlexItem grow={{default: 'grow'}}>
      <div ref={container}>
        <canvas height={canvasMeta.height} width={canvasMeta.width} ref={mainCanvas}
                style={{minWidth: 11 * canvasMeta.cellWidth + 2 * canvasMeta.sideOffset}}/>
        <canvas height={canvasMeta.height} width={canvasMeta.width} style={{display: "none"}}
                ref={hiddenCanvas}/>
      </div>
      <div ref={tooltip} style={{
        position: "absolute",
        display: "inline-block",
        padding: "10px",
        background: "#fff",
        color: "#000",
        border: "1px solid #999",
        opacity: 0,
        borderRadius: "2px",
        zIndex: 100,
      }}/>
    </FlexItem>
    {sliderMax > 0 && container.current && <FlexItem>
      <Slider value={first} min={0} max={sliderMax} step={1} showBoundaries={false} onChange={onChange}/>
    </FlexItem>}
    <FlexItem>
      <TextContent>
        <Text component={TextVariants.h3} style={{textAlign: "center"}}>{description}</Text>
      </TextContent>
    </FlexItem>
  </Flex>;
};
