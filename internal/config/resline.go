package config

// PanelReleaseRepo is the GitHub repository every panel self-update reads
// releases and update.sh from. The resline fork points it at itself so that no
// update path in the panel can install an upstream build.
const PanelReleaseRepo = "resline-networks/3x-ui"
