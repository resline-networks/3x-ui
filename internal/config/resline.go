package config

// PanelReleaseRepo is the GitHub repository every panel self-update reads
// releases and update.sh from. The resline fork points it at itself so that no
// update path in the panel can install an upstream build.
const PanelReleaseRepo = "resline-networks/3x-ui"

// XrayReleaseRepo is the GitHub repository "Switch Xray version" lists and
// downloads from: the resline xray fork, never upstream XTLS/Xray-core, whose
// builds would silently drop the fork's changes.
const XrayReleaseRepo = "resline-networks/Xray-core"
