#!/usr/bin/env python3
"""KOReader half of the setup-guide screenshots.

It starts a desktop KOReader under Xvfb with a throwaway configuration, drops
the downloaded setup patch where a real device keeps it, lets KOReader apply
it, and photographs what a reader would see: the OPDS catalog that now lists
the server, the library behind it and the Progress sync menu.

Run by scripts/guide-shots.sh. Development helpers:

    koreader.py start     start Xvfb and KOReader (state in $KO_STATE)
    koreader.py click X Y
    koreader.py shot FILE
    koreader.py stop
"""
import os
import shutil
import signal
import subprocess
import sys
import time

from Xlib import X, display
from Xlib.ext import xtest

W, H = 758, 1024
DISPLAY = os.environ.get("KO_DISPLAY", ":97")
KOREADER_DIR = os.environ.get("KOREADER_DIR", "")
STATE = os.environ.get("KO_STATE", "/tmp/ko-state")
PIDS = os.path.join(STATE, "pids")
BOOKS = os.environ.get("KO_BOOKS") or os.path.join(STATE, "Books")


def seed(unzipped):
    """Lay out a fresh KOReader data directory the way a new device has it."""
    data = os.path.join(STATE, "koreader")
    shutil.rmtree(data, ignore_errors=True)
    os.makedirs(os.path.join(data, "plugins"))
    os.makedirs(BOOKS, exist_ok=True)
    with open(os.path.join(data, "settings.reader.lua"), "w") as f:
        f.write('return {\n    ["quickstart_shown_version"] = 2099000000,\n'
                '    ["home_dir"] = %s,\n    ["lastdir"] = %s,\n    ["language"] = "en",\n}\n'
                % (lua(BOOKS), lua(BOOKS)))
    if unzipped:
        shutil.copytree(os.path.join(unzipped, "plugins", "liseur-setup.koplugin"),
                        os.path.join(data, "plugins", "liseur-setup.koplugin"))


def lua(s):
    return '"' + s.replace("\\", "\\\\").replace('"', '\\"') + '"'


def start():
    os.makedirs(STATE, exist_ok=True)
    xvfb = subprocess.Popen(["Xvfb", DISPLAY, "-screen", "0", f"{W}x{H}x24"],
                            stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
    time.sleep(1)
    env = dict(os.environ, DISPLAY=DISPLAY, XDG_CONFIG_HOME=STATE, KO_MULTIUSER="1",
               EMULATE_READER_W=str(W), EMULATE_READER_H=str(H), SDL_VIDEODRIVER="x11",
               LC_ALL="en_US.UTF-8")
    log = open(os.path.join(STATE, "koreader.log"), "w")
    ko = subprocess.Popen(["./reader.lua"], cwd=KOREADER_DIR, env=env, stdout=log, stderr=log,
                          start_new_session=True)
    with open(PIDS, "w") as f:
        f.write(f"{xvfb.pid} {ko.pid}\n")
    time.sleep(8)
    return xvfb, ko


def stop():
    try:
        pids = open(PIDS).read().split()
    except FileNotFoundError:
        return
    for p in pids:
        try:
            os.killpg(int(p), signal.SIGTERM)
        except (ProcessLookupError, PermissionError):
            try:
                os.kill(int(p), signal.SIGTERM)
            except ProcessLookupError:
                pass
    os.remove(PIDS)


def click(x, y, hold=0.08):
    d = display.Display(DISPLAY)
    xtest.fake_input(d, X.MotionNotify, x=x, y=y)
    d.sync()
    time.sleep(0.05)
    xtest.fake_input(d, X.ButtonPress, 1)
    d.sync()
    time.sleep(hold)
    xtest.fake_input(d, X.ButtonRelease, 1)
    d.sync()
    d.close()
    time.sleep(1.4)


def key(name):
    d = display.Display(DISPLAY)
    from Xlib import XK
    code = d.keysym_to_keycode(XK.string_to_keysym(name))
    xtest.fake_input(d, X.KeyPress, code)
    xtest.fake_input(d, X.KeyRelease, code)
    d.sync()
    d.close()
    time.sleep(1.0)


def shot(path):
    os.makedirs(os.path.dirname(os.path.abspath(path)), exist_ok=True)
    subprocess.run(["import", "-display", DISPLAY, "-window", "root", path], check=True)
    print("shot", path)


def main():
    cmd = sys.argv[1] if len(sys.argv) > 1 else "run"
    if cmd == "start":
        seed(os.environ.get("UNZIPPED"))
        start()
    elif cmd == "click":
        click(int(sys.argv[2]), int(sys.argv[3]))
    elif cmd == "key":
        key(sys.argv[2])
    elif cmd == "shot":
        shot(sys.argv[2])
    elif cmd == "stop":
        stop()
    elif cmd == "run":
        import sequence
        sequence.run(sys.modules[__name__])


if __name__ == "__main__":
    main()
