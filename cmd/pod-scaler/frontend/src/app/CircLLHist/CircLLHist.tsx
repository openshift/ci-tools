/*
 * Copyright (c) 2012-2016, Circonus, Inc. All rights reserved.
 *
 * Redistribution and use in source and binary forms, with or without
 * modification, are permitted provided that the following conditions are
 * met:
 *
 *     * Redistributions of source code must retain the above copyright
 *       notice, this list of conditions and the following disclaimer.
 *     * Redistributions in binary form must reproduce the above
 *       copyright notice, this list of conditions and the following
 *       disclaimer in the documentation and/or other materials provided
 *       with the distribution.
 *     * Neither the name Circonus, Inc. nor the names of its contributors
 *       may be used to endorse or promote products derived from this
 *       software without specific prior written permission.
 *
 * THIS SOFTWARE IS PROVIDED BY THE COPYRIGHT HOLDERS AND CONTRIBUTORS
 * "AS IS" AND ANY EXPRESS OR IMPLIED WARRANTIES, INCLUDING, BUT NOT
 * LIMITED TO, THE IMPLIED WARRANTIES OF MERCHANTABILITY AND FITNESS FOR
 * A PARTICULAR PURPOSE ARE DISCLAIMED. IN NO EVENT SHALL THE COPYRIGHT
 * OWNER OR CONTRIBUTORS BE LIABLE FOR ANY DIRECT, INDIRECT, INCIDENTAL,
 * SPECIAL, EXEMPLARY, OR CONSEQUENTIAL DAMAGES (INCLUDING, BUT NOT
 * LIMITED TO, PROCUREMENT OF SUBSTITUTE GOODS OR SERVICES; LOSS OF USE,
 * DATA, OR PROFITS; OR BUSINESS INTERRUPTION) HOWEVER CAUSED AND ON ANY
 * THEORY OF LIABILITY, WHETHER IN CONTRACT, STRICT LIABILITY, OR TORT
 * (INCLUDING NEGLIGENCE OR OTHERWISE) ARISING IN ANY WAY OUT OF THE USE
 * OF THIS SOFTWARE, EVEN IF ADVISED OF THE POSSIBILITY OF SUCH DAMAGE.
 */

import {Buffer} from 'buffer';

/** A histogram stores values in bins that bound the storage error for insertion
 * as well as after composition. */
export interface Histogram {
    bins: Bin[];
}

/** Determines the most positive and most negative values that this histogram contains */
export const ValueBounds = (histogram: Histogram): [minimum: number, maximum: number] => {
    let minimum: number = Number.POSITIVE_INFINITY;
    let maximum: number = Number.NEGATIVE_INFINITY;
    for (const bin of histogram.bins) {
        if (IsNan(bin)) {
            continue
        }
        if (bin.count === 0) {
            continue
        }
        const left: number = Left(bin);
        const right: number = Right(bin);
        if (left === 0) {
            continue;
        }
        if (left < minimum) {
            minimum = left;
        }
        if (right > maximum) {
            maximum = right;
        }
    }
    return [minimum, maximum];
}

/** A bin stores a count within a width. */
export interface Bin {
    count: number;
    value: number,
    exponent: number,
}

const IsNan = (b: Bin): boolean => {
    const absoluteValue: number = Math.abs(b.value);
    if (absoluteValue > 99) {
        return true;  // in [100... ]: nan
    }
    if (absoluteValue > 9) {
        return false; // in [10 - 99]: valid range
    }
    if (absoluteValue > 0) {
        return false; // in [1 - 9]:   nan
    }
    if (absoluteValue == 0) {
        return false; // in [0]:       zero bucket
    }
    return false;
}

const PowerOfTen = (b: Bin): number => {
    return powersOfTen[b.exponent];
}

const powersOfTen: number[] = [
    1, 10, 100, 1000, 10000, 100000, 1e+06, 1e+07, 1e+08, 1e+09, 1e+10,
    1e+11, 1e+12, 1e+13, 1e+14, 1e+15, 1e+16, 1e+17, 1e+18, 1e+19, 1e+20,
    1e+21, 1e+22, 1e+23, 1e+24, 1e+25, 1e+26, 1e+27, 1e+28, 1e+29, 1e+30,
    1e+31, 1e+32, 1e+33, 1e+34, 1e+35, 1e+36, 1e+37, 1e+38, 1e+39, 1e+40,
    1e+41, 1e+42, 1e+43, 1e+44, 1e+45, 1e+46, 1e+47, 1e+48, 1e+49, 1e+50,
    1e+51, 1e+52, 1e+53, 1e+54, 1e+55, 1e+56, 1e+57, 1e+58, 1e+59, 1e+60,
    1e+61, 1e+62, 1e+63, 1e+64, 1e+65, 1e+66, 1e+67, 1e+68, 1e+69, 1e+70,
    1e+71, 1e+72, 1e+73, 1e+74, 1e+75, 1e+76, 1e+77, 1e+78, 1e+79, 1e+80,
    1e+81, 1e+82, 1e+83, 1e+84, 1e+85, 1e+86, 1e+87, 1e+88, 1e+89, 1e+90,
    1e+91, 1e+92, 1e+93, 1e+94, 1e+95, 1e+96, 1e+97, 1e+98, 1e+99, 1e+100,
    1e+101, 1e+102, 1e+103, 1e+104, 1e+105, 1e+106, 1e+107, 1e+108, 1e+109,
    1e+110, 1e+111, 1e+112, 1e+113, 1e+114, 1e+115, 1e+116, 1e+117, 1e+118,
    1e+119, 1e+120, 1e+121, 1e+122, 1e+123, 1e+124, 1e+125, 1e+126, 1e+127,
    1e-128, 1e-127, 1e-126, 1e-125, 1e-124, 1e-123, 1e-122, 1e-121, 1e-120,
    1e-119, 1e-118, 1e-117, 1e-116, 1e-115, 1e-114, 1e-113, 1e-112, 1e-111,
    1e-110, 1e-109, 1e-108, 1e-107, 1e-106, 1e-105, 1e-104, 1e-103, 1e-102,
    1e-101, 1e-100, 1e-99, 1e-98, 1e-97, 1e-96,
    1e-95, 1e-94, 1e-93, 1e-92, 1e-91, 1e-90, 1e-89, 1e-88, 1e-87, 1e-86,
    1e-85, 1e-84, 1e-83, 1e-82, 1e-81, 1e-80, 1e-79, 1e-78, 1e-77, 1e-76,
    1e-75, 1e-74, 1e-73, 1e-72, 1e-71, 1e-70, 1e-69, 1e-68, 1e-67, 1e-66,
    1e-65, 1e-64, 1e-63, 1e-62, 1e-61, 1e-60, 1e-59, 1e-58, 1e-57, 1e-56,
    1e-55, 1e-54, 1e-53, 1e-52, 1e-51, 1e-50, 1e-49, 1e-48, 1e-47, 1e-46,
    1e-45, 1e-44, 1e-43, 1e-42, 1e-41, 1e-40, 1e-39, 1e-38, 1e-37, 1e-36,
    1e-35, 1e-34, 1e-33, 1e-32, 1e-31, 1e-30, 1e-29, 1e-28, 1e-27, 1e-26,
    1e-25, 1e-24, 1e-23, 1e-22, 1e-21, 1e-20, 1e-19, 1e-18, 1e-17, 1e-16,
    1e-15, 1e-14, 1e-13, 1e-12, 1e-11, 1e-10, 1e-09, 1e-08, 1e-07, 1e-06,
    1e-05, 0.0001, 0.001, 0.01, 0.1
];

/** Determines the value of the smaller boundary of the bin. For instance
 * if a bin is [x,x+n] where x and n are positive the value is x. However,
 * if x and n are negative, the value is still x. */
export const Value = (b: Bin): number => {
    if (IsNan(b)) {
        return NaN;
    }
    if (-10 < b.value && b.value < 10) {
        return 0.0;
    }
    return b.value / 10.0 * PowerOfTen(b);
};

/** Determines the width of a bin. */
export const Width = (b: Bin): number => {
    if (IsNan(b)) {
        return NaN;
    }
    if (-10 < b.value && b.value < 10) {
        return 0.0;
    }
    return PowerOfTen(b) / 10.0;
}

/** Determines the side of the bin closest to -inf. */
export const Left = (b: Bin): number => {
    if (IsNan(b)) {
        return NaN;
    }
    const left: number = Value(b);
    if (left >= 0) {
        return left;
    }
    return left - Width(b);
}

/** Determines the side of the bin closest to +inf. */
export const Right = (b: Bin): number => {
    if (IsNan(b)) {
        return NaN;
    }
    const right: number = Value(b);
    if (right < 0) {
        return right;
    }
    return right + Width(b);
}

/*
Histograms are serialized simply as a int16 header defining the number
of bins, followed by the series of bins. Each bin contains four items:
the bin's bounds (value and exponent), the size of the bin's count and
the count itself. By definition, the bin's value, exponent and the number
of bytes in the count are all eight-bit values. The layout of a bin in
the serialized byte stream is therefore:

|---bin  value---|--bin exponent--|---count size---|- bin count ...
|-----byte 1-----|-----byte 2-----|-----byte 3-----|-----byte 4 ...

Histograms use Big-Endian encoding for the bin header and bin count.
 */

/** Serialize a histogram to the buffer, at the given offset. Returns the offset
 * for the end of the range that was written.
 */
export const SerializeHistogram = (histogram: Histogram, buffer: Buffer, offset: number): number => {
    offset = buffer.writeUInt16BE(histogram.bins.length, offset);
    for (const bin of histogram.bins) {
        offset = SerializeBin(bin, buffer, offset);
    }
    return offset;
}

/** Serialize a bin to the buffer, at the given offset. Returns the offset
 * for the end of the range that was written.
 */
const SerializeBin = (bin: Bin, buffer: Buffer, offset: number): number => {
    const byteLength: number = minimalByteLength(bin.count);
    offset = buffer.writeInt8(bin.value, offset);
    offset = buffer.writeUInt8(bin.exponent, offset);
    offset = buffer.writeUInt8(byteLength, offset);
    offset = buffer.writeUIntBE(bin.count, offset, byteLength);
    return offset;
}

/** Determine the minimal number of bytes required to represent n, minus one. */
const minimalByteLength = (n: number): number => {
    let bytes = 0;
    while (n > 0) {
        n = n >> 8;
        bytes++;
    }
    return bytes - 1;
}

/** Deserialize a histogram from the buffer at the given offset. The entire contents of the buffer
 * are expected to be bins for the histogram.
 */
export const DeserializeHistogram = (buffer: Buffer): Histogram => {
    const bins: Bin[] = [];
    let offset = 0;
    const numBins: number = buffer.readUInt16BE(offset);
    offset += 2;
    for (let i = 0; i <numBins; i++) {
        let b: Bin;
        [b, offset] = DeserializeBin(buffer, offset);
        bins.push(b)
    }
    return {bins: bins};
}

/** Deserialize a bin from the buffer at the given offset. Returns the offset for the last
 * byte read and the new bin.
 */
const DeserializeBin = (buffer: Buffer, offset: number): [bin: Bin, offset: number] => {
    const b: Bin = {
        count: 0,
        value: 0,
        exponent: 0,
    };
    b.value = buffer.readInt8(offset);
    offset+=1;
    b.exponent = buffer.readUInt8(offset);
    offset+=1;
    const byteLength: number = buffer.readUInt8(offset) + 1;
    offset+=1;
    b.count = buffer.readUIntBE(offset, byteLength);
    offset+=byteLength;
    return [b, offset];
}
