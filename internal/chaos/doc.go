// Package chaos holds the zero-data-loss tests (PRD G2) that run ChitraGit
// under failure: a sync killed at random points and a sync on a full disk.
// It has no code of its own; everything is in its tests. Dropped
// connections and odd file names are tested in the syncer package, and
// the parsers have fuzz targets next to them. The nightly workflow runs
// all of it for longer.
package chaos
