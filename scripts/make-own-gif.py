#!/usr/bin/env python3
# Generates docs/own.gif — the "own a machine once, then skip the PIN" demo, in
# the same synthetic-terminal style as make-demo-gif.py.
from PIL import Image, ImageDraw, ImageFont
import os

W, H = 1000, 560
BG     = (13, 17, 23)
PANEL  = (22, 27, 34)
BORDER = (48, 54, 61)
TITLE  = (30, 36, 44)
DIM    = (128, 138, 148)
GREEN  = (63, 185, 80)
BLUE   = (88, 166, 255)
CYAN   = (86, 214, 214)
AMBER  = (210, 153, 34)
WHITE  = (233, 240, 246)
FLASH  = (33, 58, 38)

FP = '/System/Library/Fonts/Menlo.ttc'
def F(sz): return ImageFont.truetype(FP, sz)
m12, m11, m10, s9, big = F(12), F(11), F(10), F(9), F(16)

DEVICE  = (26, 96, 490, 500)
MACHINE = (510, 96, 974, 500)

OWNER_ID = 'rmnl_mQGxIWirItOnbOtr8Ku6wd_OONZLSSIiTKdISeWmOZM'
OWNER_SHORT = 'rmnl_mQGxIWir…SeWmOZM'  # truncated for the paste line so it fits the window
FP_SHORT = 'dv_2fc8bc03'
ADD_CMD  = 'sudo reminal add owner ' + OWNER_SHORT

def rr(d, box, r, fill, outline=None, w=1):
    d.rounded_rectangle(box, radius=r, fill=fill, outline=outline, width=w)

def window(d, box, title, r=9):
    x0, y0, x1, y1 = box
    rr(d, box, r, PANEL, BORDER, 1)
    rr(d, (x0, y0, x1, y0+24), r, TITLE); d.rectangle((x0+1, y0+14, x1-1, y0+24), fill=TITLE)
    for i, c in enumerate([(237, 106, 94), (245, 191, 79), (98, 197, 84)]):
        d.ellipse((x0+11+i*15, y0+8, x0+19+i*15, y0+16), fill=c)
    d.text(((x0+x1)/2, y0+12), title, font=s9, fill=DIM, anchor='mm')
    return x0 + 14, y0 + 34

def seg(d, x, y, parts, font):
    for t, c in parts:
        d.text((x, y), t, font=font, fill=c); x += d.textlength(t, font=font)
    return x

def label(d, box, text):
    x0, y0, x1, _ = box
    d.text(((x0+x1)/2, y0-15), text, font=m10, fill=DIM, anchor='mm')

def prompt(host, cmd='', hcol=GREEN):
    l = [(host + ' ', hcol), ('% ', DIM)]
    if cmd:
        l.append((cmd, WHITE))
    return l

def draw_shell(d, x, y, lines, font, cursor=False, lh=17, flash_rows=(), fw=0):
    for i, line in enumerate(lines):
        if i in flash_rows:
            d.rectangle((x-6, y-2, x+fw, y+lh-2), fill=FLASH)
        cx = x
        for t, c in line:
            d.text((cx, y), t, font=font, fill=c); cx += d.textlength(t, font=font)
        if cursor and i == len(lines)-1:
            d.rectangle((cx+2, y+2, cx+8, y+font.size+2), fill=GREEN)
        y += lh
    return y

def render(st):
    img = Image.new('RGB', (W, H), BG); d = ImageDraw.Draw(img)
    x = seg(d, 26, 22, [('re', WHITE), ('minal', BLUE)], big)
    d.text((x+14, 26), 'own a machine once — then connect with no PIN', font=m10, fill=DIM)

    # ---------- DEVICE (your laptop/phone — an owner) ----------
    label(d, DEVICE, 'your device · an owner')
    ox, oy = window(d, DEVICE, 'this device — zsh')
    draw_shell(d, ox, oy, st.get('dev', []), m11, st.get('dev_cursor', False),
               flash_rows=st.get('dev_flash', ()), fw=DEVICE[2]-DEVICE[0]-24)

    # ---------- MACHINE (the box you want to own) ----------
    label(d, MACHINE, 'the machine you want to own')
    ox2, oy2 = window(d, MACHINE, 'studio-mac — zsh')
    draw_shell(d, ox2, oy2, st.get('mac', []), m11, st.get('mac_cursor', False),
               flash_rows=st.get('mac_flash', ()), fw=MACHINE[2]-MACHINE[0]-24)

    if st.get('caption'):
        d.text((W/2, H-22), st['caption'], font=m12, fill=st.get('cap_color', DIM), anchor='mm')
    return img

frames, durs = [], []
base = dict(dev=[], mac=[], caption='')
def add(st, ms): frames.append(render({**base, **st})); durs.append(ms)

# ---- device: reminal own ----
OWN = 'reminal own'
for i in range(1, len(OWN)+1):
    add(dict(dev=[prompt('you', OWN[:i])], dev_cursor=True,
             caption='On your device, run  reminal own'), 55)
DEV_OWN = [
    prompt('you', OWN), [('', WHITE)],
    [("  This device's owner id  ", WHITE), ('(safe to share)', DIM)], [('', WHITE)],
    [('    ' + OWNER_ID, CYAN)], [('', WHITE)],
    [('  Shown as ', DIM), (FP_SHORT, GREEN), (' in ', DIM), ('reminal owners', WHITE), ('.', DIM)],
]
add(dict(dev=DEV_OWN, caption='It prints this device’s owner id (a public key)'), 1400)

# ---- machine: sudo reminal add owner <id> ----
for i in range(0, len(ADD_CMD)+1, 3):
    add(dict(dev=DEV_OWN, mac=[prompt('studio-mac', ADD_CMD[:i], hcol=BLUE)], mac_cursor=True,
             caption='Paste it on the machine — once  (needs sudo)'), 40)
add(dict(dev=DEV_OWN, mac=[prompt('studio-mac', ADD_CMD, hcol=BLUE)], mac_cursor=True,
        caption='Paste it on the machine — once  (needs sudo)'), 500)
MAC_DONE = [
    prompt('studio-mac', ADD_CMD, hcol=BLUE), [('', WHITE)],
    [('  ✓ Added owner ', GREEN), (FP_SHORT, WHITE), ('  (' + FP_SHORT + ')', DIM)],
]
for k in range(2):
    add(dict(dev=DEV_OWN, mac=MAC_DONE, mac_flash=(2,) if k == 0 else (),
             caption='Enrolled. That’s the only time you touch the machine.', cap_color=GREEN), 320)
add(dict(dev=DEV_OWN, mac=MAC_DONE, caption='Enrolled. That’s the only time you touch the machine.', cap_color=GREEN), 900)

# ---- device: reminal machines (the payoff — no PIN) ----
MACHINES = 'reminal machines'
for i in range(1, len(MACHINES)+1):
    add(dict(dev=[prompt('you', MACHINES[:i])], dev_cursor=True, mac=MAC_DONE,
             caption='From now on, no PIN — reminal machines reaches them all'), 45)
DEV_MACHINES = [
    prompt('you', MACHINES), [('', WHITE)],
    [('  Machines this device owns (2) — ', WHITE), ('2 online', GREEN)], [('', WHITE)],
    [('  ● ', GREEN), ('studio-mac', WHITE), ('  · 3', DIM)],
    [('      A7K2QX  ', WHITE), ('vim ~/api', DIM), ('        1 viewer', DIM)],
    [('      B3M9LZ  ', WHITE), ('npm run dev', DIM), ('      idle 2m', DIM)],
    [('  ● ', GREEN), ('aws-eu-1', WHITE), ('  · 1', DIM)],
    [('      C5P1RT  ', WHITE), ('backup.sh', DIM), ('        idle 1h', DIM)],
]
add(dict(dev=DEV_MACHINES, mac=MAC_DONE,
        caption='Every machine you own, every live session — no PIN, ever', cap_color=GREEN), 2200)

out = os.path.join(os.path.dirname(__file__), '..', 'docs', 'own.gif')
frames[0].save(out, save_all=True, append_images=frames[1:], duration=durs, loop=0, optimize=True, disposal=2)
print('frames', len(frames), 'total_ms', sum(durs), 'size', round(os.path.getsize(out)/1024), 'KB', '->', out)
frames[-1].save('/tmp/own_final.png')
