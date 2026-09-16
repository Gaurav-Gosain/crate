"""Run a TUI in a pty, feed its output through a terminal emulator, and
render the resulting screen to a PNG so it can actually be looked at."""
import os, pty, sys, time, select, fcntl, termios, struct
import pyte
from PIL import Image, ImageDraw, ImageFont

CMD   = sys.argv[1]
OUT   = sys.argv[2]
COLS  = int(sys.argv[3]) if len(sys.argv) > 3 else 110
ROWS  = int(sys.argv[4]) if len(sys.argv) > 4 else 34
SECS  = float(sys.argv[5]) if len(sys.argv) > 5 else 3.0
KEYS  = sys.argv[6].encode() if len(sys.argv) > 6 else b""

pid, fd = pty.fork()
if pid == 0:
    os.environ["TERM"] = "xterm-256color"
    os.environ["COLORTERM"] = "truecolor"
    os.execvp("/bin/sh", ["/bin/sh", "-c", CMD])

fcntl.ioctl(fd, termios.TIOCSWINSZ, struct.pack("HHHH", ROWS, COLS, 0, 0))

screen = pyte.Screen(COLS, ROWS)
stream = pyte.ByteStream(screen)

deadline = time.time() + SECS
sent = False
while time.time() < deadline:
    r, _, _ = select.select([fd], [], [], 0.1)
    if r:
        try:
            data = os.read(fd, 65536)
        except OSError:
            break
        if not data:
            break
        stream.feed(data)
    if KEYS and not sent and time.time() > deadline - SECS / 2:
        # A "~" in the key string waits a second before the next key. Some
        # views need time to load before the next keystroke means anything:
        # pressing enter before the library has arrived selects nothing.
        for chunk in KEYS.split(b"~"):
            if chunk:
                os.write(fd, chunk)
            time.sleep(1.0)
        sent = True

try:
    os.write(fd, b"q")
    time.sleep(0.2)
except OSError:
    pass
os.close(fd)

# xterm 256 palette, enough for what these programs emit
def xterm(n):
    if n < 16:
        base = [(0,0,0),(205,0,0),(0,205,0),(205,205,0),(0,0,238),(205,0,205),
                (0,205,205),(229,229,229),(127,127,127),(255,0,0),(0,255,0),
                (255,255,0),(92,92,255),(255,0,255),(0,255,255),(255,255,255)]
        return base[n]
    if n < 232:
        n -= 16
        lv = [0,95,135,175,215,255]
        return (lv[n//36], lv[(n//6)%6], lv[n%6])
    v = 8 + (n-232)*10
    return (v,v,v)

NAMED = {"black":(30,32,38),"red":(224,108,117),"green":(152,195,121),
         "brown":(229,192,123),"blue":(97,175,239),"magenta":(198,120,221),
         "cyan":(86,182,194),"white":(220,223,228),
         "brightblack":(92,99,112),"default":(220,223,228)}

def col(spec, default):
    if spec in (None, "default"):
        return default
    if isinstance(spec, str) and len(spec) == 6:
        try: return tuple(int(spec[i:i+2],16) for i in (0,2,4))
        except ValueError: pass
    if isinstance(spec, str) and spec.isdigit():
        return xterm(int(spec))
    return NAMED.get(spec, default)

FS = 17
try:
    font  = ImageFont.truetype("/System/Library/Fonts/Menlo.ttc", FS)
    bfont = ImageFont.truetype("/System/Library/Fonts/Menlo.ttc", FS)
except OSError:
    font = bfont = ImageFont.load_default()

CW, CH = 10, 22
PAD = 16
BG = (22, 24, 29)
img = Image.new("RGB", (COLS*CW + PAD*2, ROWS*CH + PAD*2), BG)
d = ImageDraw.Draw(img)

for y in range(ROWS):
    line = screen.buffer[y]
    for x in range(COLS):
        ch = line[x]
        bg = col(ch.bg, BG)
        if bg != BG:
            d.rectangle([PAD+x*CW, PAD+y*CH, PAD+(x+1)*CW, PAD+(y+1)*CH], fill=bg)
        if ch.data and ch.data != " ":
            fg = col(ch.fg, (220,223,228))
            if ch.bold and isinstance(ch.fg, str) and ch.fg in NAMED:
                fg = tuple(min(255, c+40) for c in fg)
            d.text((PAD+x*CW, PAD+y*CH+2), ch.data, font=bfont if ch.bold else font, fill=fg)

img.save(OUT)
print(f"  wrote {OUT}  {img.size[0]}x{img.size[1]}")
