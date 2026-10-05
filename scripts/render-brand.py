"""Rasterise the Adroit brand assets.

The SVGs in assets/brand are pure M/L/Z polygons, so a scanline even-odd fill is
all that is needed and this has no dependencies -- which is the point: nothing
here should require a rasteriser to be installed to regenerate the mark.

  python3 scripts/render-brand.py mark      # the braille splash for ui/consts.go
  python3 scripts/render-brand.py mark-small # the compact mark for the help header
  python3 scripts/render-brand.py icon OUT  # a 256px RGBA PNG for a tab icon
"""
import re


def load(path):
    src = open(path).read()
    vb = [float(v) for v in re.search(r'viewBox="([^"]+)"', src).group(1).split()]
    polys = []
    for d in re.findall(r'\sd="([^"]+)"', src):
        for chunk in d.split('M')[1:]:
            nums = [float(n) for n in re.findall(r'-?\d+\.?\d*', chunk)]
            pts = list(zip(nums[0::2], nums[1::2]))
            if len(pts) >= 3:
                polys.append(pts)
    return vb, polys


def coverage(polys, vb, w, h, ss=3):
    """Even-odd fill into a w*h grid of 0..1 coverage, ss*ss supersampled."""
    _, _, vw, vh = vb
    sx, sy = w * ss / vw, h * ss / vh
    edges = []
    for pts in polys:
        for i in range(len(pts)):
            x0, y0 = pts[i]
            x1, y1 = pts[(i + 1) % len(pts)]
            if y0 != y1:
                edges.append((x0 * sx, y0 * sy, x1 * sx, y1 * sy))

    hits = [0] * (w * h)
    for row in range(h * ss):
        yc = row + 0.5
        xs = []
        for x0, y0, x1, y1 in edges:
            if (y0 <= yc < y1) or (y1 <= yc < y0):
                xs.append(x0 + (yc - y0) * (x1 - x0) / (y1 - y0))
        xs.sort()
        for i in range(0, len(xs) - 1, 2):
            a, b = int(xs[i] + 0.5), int(xs[i + 1] + 0.5)
            for col in range(max(0, a), min(w * ss, b)):
                hits[(row // ss) * w + col // ss] += 1

    n = ss * ss
    return [min(1.0, v / n) for v in hits]


BRAILLE = [(0, 0, 0x01), (0, 1, 0x02), (0, 2, 0x04), (1, 0, 0x08),
           (1, 1, 0x10), (1, 2, 0x20), (0, 3, 0x40), (1, 3, 0x80)]


def braille(cov, w, h, cols, rows, thresh=0.5):
    out = []
    for r in range(rows):
        line = ''
        for c in range(cols):
            bits = 0
            for dx, dy, bit in BRAILLE:
                x, y = c * 2 + dx, r * 4 + dy
                if x < w and y < h and cov[y * w + x] >= thresh:
                    bits |= bit
            line += chr(0x2800 + bits)
        out.append(line.rstrip())
    return out


def halfblock(cov, w, h, cols, rows, thresh=0.5):
    out = []
    for r in range(rows):
        line = ''
        for c in range(cols):
            top = cov[(r * 2) * w + c] >= thresh if r * 2 < h else False
            bot = cov[(r * 2 + 1) * w + c] >= thresh if r * 2 + 1 < h else False
            line += {(True, True): '█', (True, False): '▀',
                     (False, True): '▄', (False, False): ' '}[(top, bot)]
        out.append(line.rstrip())
    return out


RAMP = ' ░▒▓█'


def shaded(cov, w, h, cols, rows):
    out = []
    for r in range(rows):
        line = ''
        for c in range(cols):
            v = 0.0
            for dy in (0, 1):
                y = r * 2 + dy
                if y < h:
                    v += cov[y * w + c]
            line += RAMP[min(len(RAMP) - 1, int(v / 2 * len(RAMP)))]
        out.append(line.rstrip())
    return out


def write_png(path, w, h, rgba_rows):
    """Minimal RGBA PNG writer -- zlib and struct are enough."""
    import struct, zlib
    raw = b''.join(b'\x00' + bytes(row) for row in rgba_rows)

    def chunk(tag, data):
        c = tag + data
        return struct.pack('>I', len(data)) + c + struct.pack('>I', zlib.crc32(c) & 0xffffffff)

    png = (b'\x89PNG\r\n\x1a\n'
           + chunk(b'IHDR', struct.pack('>IIBBBBB', w, h, 8, 6, 0, 0, 0))
           + chunk(b'IDAT', zlib.compress(raw, 9))
           + chunk(b'IEND', b''))
    open(path, 'wb').write(png)


def icon_png(path, polys, vb, size, rgb, pad=0.12):
    """Square icon: the mark centred on a transparent field, antialiased."""
    _, _, vw, vh = vb
    inner = int(size * (1 - 2 * pad))
    scale = min(inner / vw, inner / vh)
    w, h = max(1, int(vw * scale)), max(1, int(vh * scale))
    cov = coverage(polys, vb, w, h, ss=4)

    ox, oy = (size - w) // 2, (size - h) // 2
    r, g, b = rgb
    rows = []
    for y in range(size):
        row = bytearray()
        for x in range(size):
            a = 0
            if oy <= y < oy + h and ox <= x < ox + w:
                a = int(255 * cov[(y - oy) * w + (x - ox)])
            row += bytes((r, g, b, a))
        rows.append(row)
    write_png(path, size, size, rows)



if __name__ == '__main__':
    import os
    import sys

    here = os.path.dirname(os.path.abspath(__file__))
    symbol = os.path.join(here, '..', 'assets', 'brand', 'adroit-symbol.svg')
    what = sys.argv[1] if len(sys.argv) > 1 else 'mark'
    vb, polys = load(symbol)

    if what in ('mark', 'mark-small'):
        # 28x10 for the splash; 20x7 is the smallest the threaded a still reads
        # at, which is what the help header has room for beside a title.
        cols, rows = (28, 10) if what == 'mark' else (20, 7)
        cov = coverage(polys, vb, cols * 2, rows * 4)
        art = braille(cov, cols * 2, rows * 4, cols, rows)
        width = max(len(line) for line in art)
        # Padded with U+2800, not spaces: lipgloss centres a block by its widest
        # line, so ragged lines would each centre differently and tilt the mark.
        for line in art:
            print(line.ljust(width, chr(0x2800)))
    elif what == 'icon':
        out = sys.argv[2] if len(sys.argv) > 2 else 'adroit.png'
        # The interface accent, so the mark reads on a light or a dark tab bar;
        # the source SVG's own near-black would vanish on a dark one.
        icon_png(out, polys, vb, 256, (0x7D, 0x56, 0xF4))
        print('wrote', out)
    else:
        sys.exit(__doc__)
