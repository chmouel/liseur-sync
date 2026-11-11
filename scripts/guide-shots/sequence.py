"""The scripted KOReader walk: add nothing by hand, just use what the setup
patch configured, the way a reader would.

Coordinates are for the 758x1024 window KOReader.py opens.
"""
import os
import time

from Xlib import X, display
from Xlib.ext import xtest


def run(ko):
    img = os.environ["IMG_DIR"]
    unzipped = os.environ["UNZIPPED"]

    ko.stop()
    ko.seed(unzipped)
    ko.start()
    try:
        ko.click(380, 800)  # dismiss the colour-rendering notice
        ko.click(380, 40)  # open the top menu
        ko.click(335, 35)  # search tab
        ko.shot(f"{img}/ko-1-menu.png")

        ko.click(140, 693)  # OPDS catalog
        ko.shot(f"{img}/ko-2-catalogs.png")

        ko.click(300, 193)  # Liseur Sync
        time.sleep(3)
        ko.shot(f"{img}/ko-3-library.png")

        ko.click(300, 132)  # first book
        time.sleep(1)
        ko.shot(f"{img}/ko-4-download.png")
        ko.click(378, 603)  # EPUB
        time.sleep(3)
        ko.click(520, 626)  # Read now
        time.sleep(5)

        for _ in range(25):
            tap(ko, 700, 500)  # next page
        ko.shot(f"{img}/ko-5-reading.png")

        ko.click(380, 30)  # reader menu
        ko.click(335, 35)  # tools tab
        ko.shot(f"{img}/ko-6-tools.png")  # KoInsight and Progress sync are both here now

        ko.click(140, 363)  # KoInsight
        ko.shot(f"{img}/ko-7-koinsight.png")
        ko.click(60, 490)  # back up one level

        ko.click(140, 429)  # Progress sync
        ko.shot(f"{img}/ko-8-progress-sync.png")
        ko.click(280, 495)  # Push progress from this device now
        time.sleep(3)

        # The catalog's order is not fixed, so say which book was read.
        books = [f for f in os.listdir(ko.BOOKS) if f.endswith(".epub")]
        title = books[0][: -len(".epub")].split(" - ", 1)[-1] if books else ""
        with open(os.path.join(ko.STATE, "book-title"), "w") as f:
            f.write(title)
    finally:
        ko.stop()


def tap(ko, x, y):
    d = display.Display(ko.DISPLAY)
    xtest.fake_input(d, X.MotionNotify, x=x, y=y)
    d.sync()
    time.sleep(0.03)
    xtest.fake_input(d, X.ButtonPress, 1)
    d.sync()
    time.sleep(0.05)
    xtest.fake_input(d, X.ButtonRelease, 1)
    d.sync()
    d.close()
    time.sleep(0.25)
