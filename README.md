# Routeboy

<p align="center">
  <img src="assets/vibecoded.svg" alt="Vibecoded: some or all of this code was written by AI and accepted on vibes" width="800">
</p>

Your GPX tracks on a map. Drop `.gpx` files in a folder, pick the ones you want,
and Routeboy draws them. Tap the bar at the bottom for the details and the
elevation profile. You can also search places and bookmark the spots you care
about.

## Build

Routeboy uses [Fade](https://code.rbel.co/rubiojr/fade) for its widgets.
Go downloads Fade and its GLFW fork, which adds Wayland touch support:

```sh
git clone https://github.com/rubiojr/routeboy.git
cd routeboy
go run .
go run . -tracks ~/Downloads/gpx -scheme dark
```

You'll need Go 1.27.1, [Fyne's native build dependencies](https://docs.fyne.io/started/)
and the EGL development library (`mesa-libEGL-devel` on Fedora, `libegl-dev` on
Debian). `go build` leaves a `routeboy` binary you can copy anywhere.

The map is drawn with OpenGL ES 3 and needs a graphics driver that has it.

The first run needs the network: map data comes from [OMS](https://oms.rbel.co)
and is kept in your cache directory, so places you've already seen open offline.

## Flatpak

Install `flatpak` and `flatpak-builder`, then build a bundle for your machine:

```sh
git clone https://code.rbel.co/rubiojr/routeboy-flatpak.git flatpak
flatpak remote-add --user --if-not-exists flathub https://flathub.org/repo/flathub.flatpakrepo
scripts/flatpak
flatpak install --user dist/co.rbel.routeboy.flatpak
flatpak run co.rbel.routeboy
```

The build downloads the required SDK from Flathub. The Flatpak can read
`~/Tracks`; preferences and cached maps live under `~/.var/app/co.rbel.routeboy/`.
See [the packaging README](https://code.rbel.co/rubiojr/routeboy-flatpak) to use
another tracks folder.

## postmarketOS

`scripts/pmos` builds a package for postmarketOS phones running Phosh,
inside a podman container. On an x86_64 machine it needs QEMU user emulation
(`qemu-user-static-aarch64` on Fedora).

```sh
scripts/pmos               # aarch64, postmarketOS v26.06
ALPINE=edge scripts/pmos   # postmarketOS edge
```

The package and the key it was signed with land in `dist/`. Copy both to the
phone and install:

```sh
sudo cp routeboy-pmos.rsa.pub /etc/apk/keys/
sudo apk add ./routeboy-pmos.apk
```

## Android

`scripts/android` builds an APK with the [fyne](https://docs.fyne.io/started/packaging/)
tool, the Android SDK and its NDK. It also needs `rsvg-convert`.

```sh
scripts/android                 # arm64
ARCH=amd64 scripts/android      # for the emulator
adb install dist/routeboy-arm64.apk
```

Tracks are read from the `Tracks` folder of the phone's shared storage. Let
Routeboy read it, in the system settings under "All files access" or with:

```sh
adb shell appops set co.rbel.routeboy MANAGE_EXTERNAL_STORAGE allow
adb push my.gpx /sdcard/Tracks/
```

## Tracks

Routeboy reads `~/Tracks` by default. Use `-tracks` to point it somewhere else.
Subfolders are read too, hidden ones aren't.

**Tracks** lists everything it found, newest first, with length, climb and date.
Opening it again picks up new files, no restart needed.

- Type to search by track name or file name. Case and accents don't matter, so
  `penalara` finds Peñalara.
- Check a track to draw it on the map. Up to 20 at once, each in its own color.
- The location button on the right shows the whole track on the map.

Checked tracks are still there the next time you start Routeboy.

## Storage

On Linux, including postmarketOS:

| What | Default location |
| --- | --- |
| Bookmarks and checked/current track paths | `~/.config/fyne/co.rbel.routeboy/preferences.json` |
| Cached map data | `~/.cache/co.rbel.routeboy/map/` |
| GPX files | `~/Tracks/`, or the folder passed with `-tracks` |

Config and cache paths respect `XDG_CONFIG_HOME` and `XDG_CACHE_HOME`.
Routeboy reads GPX files where they are; it doesn't copy them into its storage.
The **Places** history is kept in memory and disappears when you quit.

The map cache keeps up to 256 MiB by default; change that with `-store-mib`.
If the cache directory is unavailable, map data is kept in the app's storage
directory under `map/`. If neither directory works, it stays in memory for the
session, up to 32 MiB or the configured limit, whichever is smaller.

## License

Routeboy is [MIT-licensed](LICENSE).
