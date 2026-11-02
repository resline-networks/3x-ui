package config

// PanelReleaseRepo is where every panel self-update reads releases and update.sh;
// pointing it at the fork keeps every update path from installing an upstream build.
const PanelReleaseRepo = "resline-networks/3x-ui"

// XrayReleaseRepo backs "Switch Xray version": the resline xray fork, never upstream
// XTLS/Xray-core, whose builds would silently drop the fork's changes.
const XrayReleaseRepo = "resline-networks/Xray-core"
