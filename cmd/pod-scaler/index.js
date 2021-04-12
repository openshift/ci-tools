function fetchAndRenderAllData() {
    let params = {}
    for (const id of ["org", "repo", "branch", "variant", "target", "step", "pod", "container"]) {
        params[id] = document.getElementById(id + "Input").value;
    }
    fetchAndRenderData(params, "data")
}

function fetchAndRenderStepData() {
    let params = {}
    for (const id of ["step", "container"]) {
        params[id] = document.getElementById(id + "Input").value;
    }
    fetchAndRenderData(params, "stepData")
}

function fetchAndRenderData(params, suffix) {
    history.replaceState(null, null, "?" + new URLSearchParams(params).toString());
    let url = new URL(`${window.location.protocol}//${window.location.host}/` + suffix)
    url.search = new URLSearchParams(params).toString()
    fetch(url)
        .then(async (response) => {
            const msg = await response.text();
            if (!response.ok) {
                throw msg;
            }
            let container = document.getElementById("data")
            while (container.firstChild) {
                container.firstChild.remove()
            }
            let data = JSON.parse(msg);
            updateData("suffix", data)
        });
    let info = document.getElementById("infoBox");
    info.style.display = "block";
}

function updateData(name, data) {
    let container = document.createElement("div");
    container.id = name;
    container.style.display = "flex";
    container.style.flexDirection = "row";
    container.style.justifyContent = "space-evenly";
    let cpu = {
        name: name + "_cpu",
        title: "CPU Usage",
        yAxisTitle: "CPU Used",
        yAxisUnit: "mCPU",
        yAxisMin: .005,
        yAxisFormatter: function (value) {
            return Math.round(value * 1000);
        },
        data: data["cpu"],
    };
    render(cpu, container);
    let memory = {
        name: name + "_memory",
        title: "Memory Usage",
        yAxisTitle: "Memory Used",
        yAxisUnit: "MiB",
        yAxisMin: 100 * Math.pow(2, 20),
        yAxisFormatter: function (value) {
            return Math.round(value / Math.pow(2, 20));
        },
        data: data["memory"],
    }
    render(memory, container);
    let dataContainer = document.getElementById("data");
    dataContainer.appendChild(container);
}

function render(data, container) {
    let canvasMeta = {
        sideOffset: 100,
        verticalOffset: 20,
        topOffset: 50,
        cellWidth: 20,
        dataHeight: 1000,
        data: data,
    }
    const maxColumns = Math.floor((window.innerWidth - canvasMeta.sideOffset * 4) / (2 * canvasMeta.cellWidth));
    if (data.data["histograms"].length + 1 > maxColumns) {
        canvasMeta.numCols = maxColumns
    } else {
        canvasMeta.numCols = data.data["histograms"].length + 1;
    }
    canvasMeta.width = canvasMeta.sideOffset * 2 + canvasMeta.numCols * canvasMeta.cellWidth;
    canvasMeta.height = canvasMeta.dataHeight + 2 * canvasMeta.verticalOffset + canvasMeta.topOffset;

    let subContainer = document.createElement("div");
    subContainer.id = data.name;
    subContainer.width = canvasMeta.width;
    subContainer.style.display = "flex";
    subContainer.style.flexDirection = "column";
    subContainer.style.justifyContent = "space-around";
    let mainCanvas = document.createElement('canvas');
    mainCanvas.id = "mainCanvas_" + data.name;
    mainCanvas.width = canvasMeta.width;
    mainCanvas.height = canvasMeta.height;
    mainCanvas.classList.add("mainCanvas");
    subContainer.appendChild(mainCanvas)
    let hiddenCanvas = document.createElement('canvas');
    hiddenCanvas.id = "hiddenCanvas_" + data.name;
    hiddenCanvas.width = canvasMeta.width;
    hiddenCanvas.height = canvasMeta.height;
    hiddenCanvas.classList.add("hidden");
    subContainer.appendChild(hiddenCanvas)
    let index = {};
    const bounds = boundsFor(data.data["merged"]);
    if (bounds[0] < data.yAxisMin) {
        bounds[0] = data.yAxisMin
    }
    canvasMeta.minimum = bounds[0];
    canvasMeta.maximum = bounds[1];
    canvasMeta.pixelCoordinate = function (value) {
        let fraction = (
            (Math.log10(value) - Math.log10(canvasMeta.minimum)) /
            (Math.log10(canvasMeta.maximum) - Math.log10(canvasMeta.minimum))
        )
        return canvasMeta.topOffset + canvasMeta.verticalOffset + canvasMeta.dataHeight - Math.round(fraction * canvasMeta.dataHeight);
    }
    canvasMeta.nextColor = 1;
    canvasMeta.genColor = function () {
        let ret = [];
        // via http://stackoverflow.com/a/15804183
        if (canvasMeta.nextColor < 16777215) {
            ret.push(canvasMeta.nextColor & 0xff); // R
            ret.push((canvasMeta.nextColor & 0xff00) >> 8); // G
            ret.push((canvasMeta.nextColor & 0xff0000) >> 16); // B
            canvasMeta.nextColor += 1;
        }
        return "rgb(" + ret.join(',') + ")";
    }

    let mainContext = mainCanvas.getContext('2d');
    let hiddenContext = hiddenCanvas.getContext('2d');
    drawAxes(mainContext, canvasMeta)
    addBucketsTo(data.data["merged"], index, mainContext, hiddenContext, canvasMeta.sideOffset, canvasMeta)
    mainContext.lineWidth = 1;
    mainContext.strokeStyle = "black";
    mainContext.beginPath();
    mainContext.moveTo(canvasMeta.sideOffset - 1, canvasMeta.topOffset + canvasMeta.verticalOffset - 1);
    mainContext.lineTo(canvasMeta.sideOffset + canvasMeta.cellWidth + 1, canvasMeta.topOffset + canvasMeta.verticalOffset - 1);
    mainContext.lineTo(canvasMeta.sideOffset + canvasMeta.cellWidth + 1, canvasMeta.topOffset + canvasMeta.verticalOffset + canvasMeta.dataHeight + 1);
    mainContext.lineTo(canvasMeta.sideOffset - 1, canvasMeta.topOffset + canvasMeta.verticalOffset + canvasMeta.dataHeight + 1);
    mainContext.lineTo(canvasMeta.sideOffset - 1, canvasMeta.topOffset + canvasMeta.verticalOffset - 1);
    mainContext.stroke();

    for (let i = 0; i < canvasMeta.numCols - 1; i++) {
        const offset = canvasMeta.sideOffset + (i + 1) * canvasMeta.cellWidth + 1;
        addBucketsTo(data.data["histograms"][i], index, mainContext, hiddenContext, offset, canvasMeta)
    }

    if (canvasMeta.numCols < data.data["histograms"].length) {
        let slider = document.createElement("input")
        slider.type = "range";
        slider.width = canvasMeta.width - 2 * canvasMeta.sideOffset;
        slider.min = "0";
        slider.max = (data.data["histograms"].length - canvasMeta.numCols).toString();
        slider.classList.add("slider");
        slider.id = "slider_" + data.name;
        slider.oninput = redrawData(canvasMeta, index)
        let sliderContainer = document.createElement("div");
        sliderContainer.id = "slider_container_" + data.name;
        sliderContainer.style.display = "flex";
        sliderContainer.style.flexDirection = "row";
        sliderContainer.style.justifyContent = "space-around";
        sliderContainer.appendChild(slider)
        subContainer.appendChild(sliderContainer)
    }

    let infoContainer = document.createElement("div");
    infoContainer.id = "info_" + data.name;
    infoContainer.width = canvasMeta.width - 2 * canvasMeta.sideOffset;
    const request = canvasMeta.data.yAxisFormatter(parseFloat(canvasMeta.data.data["cutoff"]))
    infoContainer.innerHTML = "Analyzing " + data.data["histograms"].length + " traces, a request of " + request + canvasMeta.data.yAxisUnit + " is recommended."
    subContainer.appendChild(infoContainer)

    mainCanvas.addEventListener("mousemove", function (event) {
        let color = hiddenContext.getImageData(event.offsetX, event.offsetY, 1, 1).data;
        const key = "rgb(" + color[0] + "," + color[1] + "," + color[2] + ")";
        let rect = document.getElementById("mainCanvas_" + data.name).getBoundingClientRect();
        let tooltip = document.getElementById("tooltip");
        if (key in index) {
            tooltip.style.opacity = "0.8";
            tooltip.style.top = rect.y + event.offsetY + 5 + "px";
            tooltip.style.left = rect.x + event.offsetX + 5 + "px";
            tooltip.innerHTML = index[key];
        } else {
            tooltip.style.opacity = "0";
        }
    })
    container.appendChild(subContainer)
}

function redrawData(canvasMeta, index) {
    return function () {
        let mainCanvas = document.getElementById("mainCanvas_" + canvasMeta.data.name);
        let hiddenCanvas = document.getElementById("hiddenCanvas_" + canvasMeta.data.name);
        let mainContext = mainCanvas.getContext('2d');
        let hiddenContext = hiddenCanvas.getContext('2d');
        clearData(mainContext, canvasMeta);
        clearData(hiddenContext, canvasMeta);
        for (let i = 0; i < canvasMeta.numCols - 1; i++) {
            const offset = canvasMeta.sideOffset + (i + 1) * canvasMeta.cellWidth + 1;
            addBucketsTo(canvasMeta.data.data["histograms"][parseInt(this.value) + i], index, mainContext, hiddenContext, offset, canvasMeta)
        }
    }
}

function clearData(context, canvasMeta) {
    context.clearRect(canvasMeta.sideOffset + canvasMeta.cellWidth, canvasMeta.topOffset + canvasMeta.verticalOffset, (canvasMeta.numCols - 1) * canvasMeta.cellWidth, canvasMeta.dataHeight);
}

function drawAxes(context, canvasMeta) {
    context.textAlign = "center";
    context.textBaseline = "top";
    context.font = "20pt Verdana";
    context.fillStyle = "black";
    context.fillText(canvasMeta.data.yAxisTitle, canvasMeta.width / 2, 10)
    drawAxis(context, canvasMeta, canvasMeta.sideOffset, -1, "right")
    drawAxis(context, canvasMeta, canvasMeta.width - canvasMeta.sideOffset, 1, "left")
}

function drawAxis(context, canvasMeta, axisXBase, sign, side) {
    const axisX = axisXBase + sign * 10;
    const axisYTop = canvasMeta.verticalOffset + canvasMeta.topOffset,
        axisYBottom = canvasMeta.verticalOffset + canvasMeta.topOffset + canvasMeta.dataHeight;
    context.font = "14pt Verdana";
    context.textAlign = side;
    context.textBaseline = "alphabetic";
    context.fillStyle = "black";
    context.fillText(canvasMeta.data.yAxisUnit, axisX, axisYTop - 15)
    lineBetween(context, 2, "black", [axisX, axisYTop], [axisX, axisYBottom])
    let majorTicks = [];
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
    majorTick(context, axisX, parseFloat(canvasMeta.data.data["cutoff"]), "red", canvasMeta, sign, side)
}

function majorTick(context, axisX, value, style, canvasMeta, sign, side) {
    const tickY = canvasMeta.pixelCoordinate(value);
    lineBetween(context, 2, style, [axisX + sign * 10, tickY], [axisX, tickY])
    context.font = "10pt Verdana";
    context.textAlign = side;
    context.textBaseline = "middle";
    context.fillStyle = style;
    context.fillText(canvasMeta.data.yAxisFormatter(value), axisX + sign * 11, tickY)
}

function minorTick(context, axisX, value, canvasMeta, sign) {
    const tickY = canvasMeta.pixelCoordinate(value);
    lineBetween(context, 1, "black", [axisX + sign * 5, tickY], [axisX, tickY])
}

function lineBetween(context, width, style, a, b) {
    context.lineWidth = width;
    context.strokeStyle = style;
    context.beginPath();
    context.moveTo(a[0], a[1]);
    context.lineTo(b[0], b[1]);
    context.stroke();
}

function boundsFor(raw) {
    let accessor = new circllhist.js.circllhist()
    let hist = new circllhist.js.circllhist()
    hist = hist.deserialize_b64(raw)
    let minimum = 1e100, maximum = 0;
    for (const bv of hist.bvs) {
        if (bv.count === 0) {
            continue
        }
        const bottom = accessor.hist_bucket_to_double(bv.bucket)
        const height = accessor.hist_bucket_to_double_bin_width(bv.bucket)
        const top = bottom + height;
        if (bottom < minimum && bottom !== 0) {
            minimum = bottom
        }
        if (top > maximum && top !== 0) {
            maximum = top
        }
    }
    return [minimum, maximum]
}

function addBucketsTo(raw, index, mainContext, hiddenContext, offset, canvasMeta) {
    let accessor = new circllhist.js.circllhist()
    let hist = new circllhist.js.circllhist()
    hist = hist.deserialize_b64(raw)
    let largest = 0;
    for (const bv of hist.bvs) {
        if (bv.count > largest) {
            largest = bv.count
        }
    }
    mainContext.fillStyle = d3.interpolateCividis(0);
    mainContext.globalCompositeOperation = "destination-over";
    mainContext.fillRect(offset, canvasMeta.verticalOffset + canvasMeta.topOffset, canvasMeta.cellWidth, canvasMeta.dataHeight)
    mainContext.globalCompositeOperation = "source-over";
    for (const bv of hist.bvs) {
        const count = bv.count
        if (count === 0) {
            continue
        }
        const bottom = accessor.hist_bucket_to_double(bv.bucket)
        const height = accessor.hist_bucket_to_double_bin_width(bv.bucket)
        if (bottom === 0 || height === 0 || bottom < canvasMeta.data.yAxisMin) {
            continue
        }
        let top = bottom + height;
        mainContext.fillStyle = d3.interpolateCividis(count / largest)
        const topY = canvasMeta.pixelCoordinate(top);
        const bottomY = canvasMeta.pixelCoordinate(bottom);
        const heightY = bottomY - topY;
        mainContext.fillRect(offset, topY, canvasMeta.cellWidth, heightY)

        let hiddenColor = canvasMeta.genColor();
        index[hiddenColor] = "(" + canvasMeta.data.yAxisFormatter(bottom) + "," + canvasMeta.data.yAxisFormatter(bottom + height) + ")" + canvasMeta.data.yAxisUnit + ": " + count + " samples";
        hiddenContext.fillStyle = hiddenColor
        hiddenContext.fillRect(offset, topY, canvasMeta.cellWidth, heightY)
    }
}

function renderIndex(data) {
    let handler = fillData(data)
    handler()
    let queryParams = new URLSearchParams(window.location.search);
    for (const id of ["org", "repo", "branch", "variant", "target", "step", "pod", "container"]) {
        let input = document.getElementById(id + "Input");
        let queryInput = queryParams.get(id);
        if (queryInput !== "") {
            input.value = queryInput;
        }
        input.addEventListener("change", handler);
    }
}

function fillData(data) {
    return function () {
        let v = {};
        for (const id of ["org", "repo", "branch", "variant", "target", "step", "pod", "container"]) {
            let input = document.getElementById(id + "Input");
            v[id] = input.value;
        }
        fillLists(data, v)
        validateInputs(data, v)
    }
}

function validateInputs(data, v) {
    if (v["org"] !== "") {
        validateInput("orgInput", Object.keys(data), v["org"])
    }
    if (v["repo"] !== "") {
        validateInput("repoInput", Object.keys(data[v["org"]]), v["repo"])
    }
    if (v["branch"] !== "") {
        validateInput("branchInput", Object.keys(data[v["org"]][v["repo"]]), v["branch"])
    }
    if (v["variant"] !== "") {
        validateInput("variantInput", Object.keys(data[v["org"]][v["repo"]][v["branch"]]), v["variant"])
    }
    if (v["target"] !== "") {
        validateInput("targetInput", Object.keys(data[v["org"]][v["repo"]][v["branch"]][v["variant"]]), v["target"])
    }
    if (v["step"] !== "") {
        validateInput("stepInput", Object.keys(data[v["org"]][v["repo"]][v["branch"]][v["variant"]][v["target"]]["containers_by_step"]), v["step"])
    }
    if (v["pod"] !== "") {
        validateInput("podInput", Object.keys(data[v["org"]][v["repo"]][v["branch"]][v["variant"]][v["target"]]["containers_by_pod"]), v["pod"])
    }
    if (v["container"] !== "") {
        if (v["step"] !== "") {
            validateInput("containerInput", data[v["org"]][v["repo"]][v["branch"]][v["variant"]][v["target"]]["containers_by_step"][v["step"]], v["container"])
        } else if (v["pod"] !== "") {
            validateInput("containerInput", data[v["org"]][v["repo"]][v["branch"]][v["variant"]][v["target"]]["containers_by_pod"][v["pod"]], v["container"])
        }
    }
}

function validateInput(id, data, value) {
    let input = document.getElementById(id);
    if (data.includes(value)) {
        input.setCustomValidity("");
    } else {
        input.setCustomValidity("Invalid - choose an option from the list.");
    }
    input.reportValidity();
}

function fillLists(data, v) {
    if (v["org"] === "") {
        fillDataListWith("orgOptions", Object.keys(data))
        return
    }
    if (v["repo"] === "") {
        fillDataListWith("repoOptions", Object.keys(data[v["org"]]))
        return
    }
    if (v["branch"] === "") {
        fillDataListWith("branchOptions", Object.keys(data[v["org"]][v["repo"]]))
        return
    }
    if (v["variant"] === "") {
        fillDataListWith("variantOptions", Object.keys(data[v["org"]][v["repo"]][v["branch"]]))
    }
    if (v["target"] === "") {
        fillDataListWith("targetOptions", Object.keys(data[v["org"]][v["repo"]][v["branch"]][v["variant"]]))
        return
    }
    if (v["step"] === "") {
        fillDataListWith("stepOptions", Object.keys(data[v["org"]][v["repo"]][v["branch"]][v["variant"]][v["target"]]["containers_by_step"]))
    } else {
        // target and step fully quality pod
        document.getElementById("podInput").value = v["target"] + "-" + v["step"];
    }
    if (v["pod"] === "") {
        fillDataListWith("podOptions", Object.keys(data[v["org"]][v["repo"]][v["branch"]][v["variant"]][v["target"]]["containers_by_pod"]))
    }
    if (v["container"] === "") {
        if (v["step"] !== "") {
            fillDataListWith("containerOptions", data[v["org"]][v["repo"]][v["branch"]][v["variant"]][v["target"]]["containers_by_step"][v["step"]])
        } else if (v["pod"] !== "") {
            fillDataListWith("containerOptions", data[v["org"]][v["repo"]][v["branch"]][v["variant"]][v["target"]]["containers_by_pod"][v["pod"]])
        }
    }
}

function fillDataListWith(id, data) {
    const options = document.getElementById(id);
    while (options.firstChild) {
        options.firstChild.remove()
    }
    for (const item of data) {
        let option = document.createElement("option");
        option.textContent = item
        options.appendChild(option)
    }
}

let loadData = fetchAndRenderAllData;

document.getElementById("query_steps").addEventListener("click", function () {
    loadData = fetchAndRenderStepData;
    toggleInputVisibility({
        "org": false,
        "repo": false,
        "branch": false,
        "variant": false,
        "target": false,
        "step": true,
        "pod": false,
        "container": true,
    })
});

document.getElementById("query_meta_step").addEventListener("click", function () {
    loadData = fetchAndRenderAllData;
    toggleInputVisibility({
        "org": true,
        "repo": true,
        "branch": true,
        "variant": true,
        "target": true,
        "step": true,
        "pod": false,
        "container": true,
    })
});

document.getElementById("query_meta_pod").addEventListener("click", function () {
    loadData = fetchAndRenderAllData;
    toggleInputVisibility({
        "org": true,
        "repo": true,
        "branch": true,
        "variant": true,
        "target": true,
        "step": false,
        "pod": true,
        "container": true,
    })
});

function toggleInputVisibility(options) {
    for (const key in options) {
        if (!options.hasOwnProperty(key)) {
            continue
        }
        let input = document.getElementById(key + "Input");
        if (options[key]) {
            input.style.display = "block";
        } else {
            input.style.display = "none";
        }
    }
}

const indexData = JSON.parse(document.getElementById('indexData').innerHTML);
renderIndex(indexData);
document.getElementById("loadButton").addEventListener("click", function () {
    loadData()
});