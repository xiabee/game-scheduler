#!/usr/bin/env python3
"""pnglite.py — minimal stdlib-only PNG reader/writer for the learning tools.

The vision tooling (ROADMAP §4 / NC9) must stay dependency-light: no PIL,
no numpy, no opencv. PNG is deflate + per-scanline filters, both inside
the standard library's reach (zlib + struct + binascii-free CRC via
zlib.crc32).

Supported subset — deliberately narrow, covering everything our own
pipeline produces and what frame extractors emit by default:
- bit depth 8
- color type 2 (RGB) and 6 (RGBA)
- no interlace
Writing always produces 8-bit RGBA with filter 0 scanlines (larger on
disk than filtered output; fine for tool-side intermediates, and the
simplest format that round-trips).
"""

from __future__ import annotations

import struct
import zlib

SIGNATURE = b"\x89PNG\r\n\x1a\n"


class PngError(ValueError):
    pass


def _chunk(kind: bytes, data: bytes) -> bytes:
    return (
        struct.pack(">I", len(data))
        + kind
        + data
        + struct.pack(">I", zlib.crc32(kind + data) & 0xFFFFFFFF)
    )


def write_png(path, width: int, height: int, rgba: bytes) -> None:
    """Write an 8-bit RGBA PNG. `rgba` is width*height*4 bytes, row-major."""
    if width <= 0 or height <= 0:
        raise PngError(f"dimensions must be positive, got {width}x{height}")
    if len(rgba) != width * height * 4:
        raise PngError(f"data size {len(rgba)} != {width}x{height}x4")

    ihdr = struct.pack(">IIBBBBB", width, height, 8, 6, 0, 0, 0)
    stride = width * 4
    raw = bytearray()
    for y in range(height):
        raw.append(0)  # filter type 0 (None)
        raw += rgba[y * stride : (y + 1) * stride]
    body = (
        _chunk(b"IHDR", ihdr)
        + _chunk(b"IDAT", zlib.compress(bytes(raw), 6))
        + _chunk(b"IEND", b"")
    )
    with open(path, "wb") as f:
        f.write(SIGNATURE)
        f.write(body)


def _paeth(a: int, b: int, c: int) -> int:
    p = a + b - c
    pa, pb, pc = abs(p - a), abs(p - b), abs(p - c)
    if pa <= pb and pa <= pc:
        return a
    if pb <= pc:
        return b
    return c


def read_png(path):
    """Read an 8-bit RGB/RGBA, non-interlaced PNG.

    Returns (width, height, channels, bytes) with channels in {3, 4};
    bytes are unfiltered, row-major pixel data.
    """
    with open(path, "rb") as f:
        blob = f.read()
    if not blob.startswith(SIGNATURE):
        raise PngError("not a PNG file (bad signature)")
    pos = len(SIGNATURE)
    width = height = 0
    channels = 0
    idat = bytearray()
    while pos + 8 <= len(blob):
        (length,) = struct.unpack_from(">I", blob, pos)
        kind = blob[pos + 4 : pos + 8]
        data = blob[pos + 8 : pos + 8 + length]
        pos += 12 + length  # length + kind + data + CRC
        if kind == b"IHDR":
            if length != 13:
                raise PngError("bad IHDR length")
            width, height, depth, ctype, comp, filt, interlace = struct.unpack(
                ">IIBBBBB", data
            )
            if depth != 8:
                raise PngError(f"unsupported bit depth {depth} (only 8)")
            if comp != 0 or filt != 0:
                raise PngError("unsupported compression/filter method")
            if interlace != 0:
                raise PngError("interlaced PNG not supported")
            channels = {2: 3, 6: 4}.get(ctype)
            if channels is None:
                raise PngError(f"unsupported color type {ctype} (only RGB/RGBA)")
        elif kind == b"IDAT":
            idat += data
        elif kind == b"IEND":
            break
    if width <= 0 or height <= 0 or channels == 0:
        raise PngError("missing or invalid IHDR")
    if not idat:
        raise PngError("no IDAT data")

    raw = zlib.decompress(bytes(idat))
    stride = width * channels
    expected = (stride + 1) * height
    if len(raw) != expected:
        raise PngError(f"decompressed size {len(raw)} != expected {expected}")

    out = bytearray(stride * height)
    bpp = channels  # bytes per pixel for filter math
    for y in range(height):
        off = y * (stride + 1)
        ftype = raw[off]
        line = raw[off + 1 : off + 1 + stride]
        prev = bytes(out[(y - 1) * stride : y * stride]) if y > 0 else bytes(stride)
        cur = bytearray(stride)
        if ftype == 0:  # None
            cur[:] = line
        elif ftype == 1:  # Sub
            for i in range(stride):
                left = cur[i - bpp] if i >= bpp else 0
                cur[i] = (line[i] + left) & 0xFF
        elif ftype == 2:  # Up
            for i in range(stride):
                cur[i] = (line[i] + prev[i]) & 0xFF
        elif ftype == 3:  # Average
            for i in range(stride):
                left = cur[i - bpp] if i >= bpp else 0
                cur[i] = (line[i] + ((left + prev[i]) >> 1)) & 0xFF
        elif ftype == 4:  # Paeth
            for i in range(stride):
                left = cur[i - bpp] if i >= bpp else 0
                upleft = prev[i - bpp] if i >= bpp else 0
                cur[i] = (line[i] + _paeth(left, prev[i], upleft)) & 0xFF
        else:
            raise PngError(f"unsupported scanline filter {ftype}")
        out[y * stride : (y + 1) * stride] = cur
    return width, height, channels, bytes(out)


def rgba_to_rgb(rgba: bytes, width: int, height: int) -> bytes:
    out = bytearray(width * height * 3)
    for i in range(width * height):
        out[i * 3 : i * 3 + 3] = rgba[i * 4 : i * 4 + 3]
    return bytes(out)
