package internal

import (
	"flag"
	"fmt"
	"log"
	"os"
	"os/exec"
	"regexp"
	"strings"
)

// user used for deploying to other servers
const DEPLOYUSER = "mediawikiuser"

// hostname; needs to be set from RunDeploy since it returns h, err
var HOSTNAME string

// path where extensions are located
const EXTENSIONPATH = "/prod/mediawiki-staging/extensions/"

// path where skins are located
const SKINPATH = "/prod/mediawiki-staging/skins/"

// bare path for staging env
const STAGINGPATH = "/prod/mediawiki-staging"

// bare path for prod env
const PRODUCTIONPATH = "/prod/mediawiki"

// the wiki database name passed to maintenance scripts
const WIKIDBNAME = "metawiki"

// runner script all maintenance scripts go through since we're beyond 1.39
var RUNNER = PRODUCTIONPATH + "/maintenance/run.php"

// valid extensions that this script can work on - a extension must exist and have a .git folder to be valid
var VALIDEXTENSIONS []string

// valid skins that this script can work on - a skin must exist and have a .git folder to be valid
var VALIDSKINS []string

// a deploy target. Name is matched against the local the local hostnamae is
// used to check whether we are runnin locally whilst the fqdn is used for the actual
// sync
type Server struct {
	Name    string
	SSHHost string
}

// all of the servers that are valid
var ALLSERVERS = []Server{
	{Name: "mw1", SSHHost: "mw1"},
	{Name: "mw2", SSHHost: "mw2"},
	{Name: "mwtask1", SSHHost: "mwtask1"},
	{Name: "jobrunner", SSHHost: "jobrunner.telepedia.internal"},
}

// all possible deploy options
type DeployConfig struct {
	UpgradeExtensions []string
	UpgradeSkins      []string
	UpgradeVendor     bool
	UpgradeWorld      bool
	L10n              bool
	Lang              string
	Servers           []Server
	IgnoreTime        bool
	Force             bool
	SyncConfig        bool
}

// actually run the deploy
func RunDeploy(args []string) {
	hname, err := os.Hostname()

	if err != nil {
		fmt.Println("Could not determine hostname...", err)
		os.Exit(1)
	}

	HOSTNAME = strings.Split(hname, ".")[0]

	config := parseFlags(args)

	VALIDEXTENSIONS = GetValidExtensions()
	VALIDSKINS = GetValidSkins()

	// --upgrade-world is a helper to do everything
	if config.UpgradeWorld {
		config.UpgradeExtensions = VALIDEXTENSIONS
		config.UpgradeSkins = VALIDSKINS
		config.UpgradeVendor = true
		config.L10n = true
		config.IgnoreTime = true
	}

	// validate our config is valid first before we do anything
	if err := validateConfig(config); err != nil {
		log.Fatal(err)
	}

	fmt.Printf("Deploying to servers: %v\n", config.Servers)

	// actually execute the deploy
	if err := executeDeploy(config); err != nil {
		log.Fatal(err)
	}

	fmt.Println("Deploy completed successfully")
}

// Parse the flags passed to the script so we know what we're doing
func parseFlags(args []string) *DeployConfig {
	deployCmd := flag.NewFlagSet("deploy", flag.ExitOnError)

	upgradeExtensions := deployCmd.String("upgrade-extensions", "", "Comma separated extensions to upgrade")
	upgradeSkins := deployCmd.String("upgrade-skins", "", "Comma separated skins to upgrade")
	upgradeVendor := deployCmd.Bool("upgrade-vendor", false, "Update vendor directory (Composer dependencies)")
	upgradeWorld := deployCmd.Bool("upgrade-world", false, "Update everything (vendor, all extensions, all skins, l10n)")
	l10n := deployCmd.Bool("l10n", false, "Rebuild localization cache")
	lang := deployCmd.String("lang", "", "Specific languages for l10n (comma-separated)")
	servers := deployCmd.String("servers", "", "Target servers (comma-separated)")
	ignoreTime := deployCmd.Bool("ignore-time", false, "Use --inplace instead of --update for rsync")
	force := deployCmd.Bool("force", false, "Force deployment even on errors")
	syncConfig := deployCmd.Bool("config", false, "Sync the entire root directory (including LocalSettings.php)")

	deployCmd.Parse(args)

	config := &DeployConfig{
		UpgradeVendor: *upgradeVendor,
		UpgradeWorld:  *upgradeWorld,
		L10n:          *l10n,
		Lang:          *lang,
		IgnoreTime:    *ignoreTime,
		Force:         *force,
		SyncConfig:    *syncConfig,
	}

	if *upgradeExtensions != "" {
		config.UpgradeExtensions = strings.Split(*upgradeExtensions, ",")
	}

	if *upgradeSkins != "" {
		config.UpgradeSkins = strings.Split(*upgradeSkins, ",")
	}

	if *servers != "" {
		resolved, err := resolveServers(*servers)
		if err != nil {
			fmt.Println(err)
			os.Exit(1)
		}
		config.Servers = resolved
	}

	return config
}

// resolve a comma-separated server string into concrete Server targets. "all"
// expands to every known server
func resolveServers(input string) ([]Server, error) {
	if input == "all" {
		return ALLSERVERS, nil
	}

	var result []Server
	for _, name := range strings.Split(input, ",") {
		srv, ok := findServer(name)
		if !ok {
			return nil, fmt.Errorf("invalid server: %s", name)
		}
		result = append(result, srv)
	}
	return result, nil
}

// find a known server by its name
func findServer(name string) (Server, bool) {
	for _, s := range ALLSERVERS {
		if s.Name == name {
			return s, true
		}
	}
	return Server{}, false
}

// validate that what the user asked for is actually valid
func validateConfig(config *DeployConfig) error {
	for _, ext := range config.UpgradeExtensions {
		if !contains(VALIDEXTENSIONS, ext) {
			return fmt.Errorf("invalid extension: %s", ext)
		}
	}

	for _, skin := range config.UpgradeSkins {
		if !contains(VALIDSKINS, skin) {
			return fmt.Errorf("invalid skin: %s", skin)
		}
	}

	if len(config.Servers) == 0 {
		return fmt.Errorf("at least one server required")
	}

	if config.Lang != "" {
		if !config.L10n {
			return fmt.Errorf("--lang requires --l10n flag")
		}
		for _, lang := range strings.Split(config.Lang, ",") {
			if !isValidLangTag(lang) {
				return fmt.Errorf("invalid language tag: %s", lang)
			}
		}
	}

	return nil
}

// lightweight BCP-47-ish check to check whether the language passed
// is likely to be valid; this isn't exhaustive and there may be edge cases
// that return false when actually valid
var langTagRe = regexp.MustCompile(`^[a-zA-Z]{2,3}(-[a-zA-Z0-9]{2,8})*$`)

func isValidLangTag(tag string) bool {
	return langTagRe.MatchString(tag)
}

// get all of the valid extensions - in order to be valid, it must exist in the extension path, and be
// a git repository
func GetValidExtensions() []string {
	var validExtensions []string
	entries, err := os.ReadDir(EXTENSIONPATH)
	if err != nil {
		log.Fatal(err)
	}

	for _, ext := range entries {
		if !ext.IsDir() {
			continue
		}
		gitPath := fmt.Sprintf("%s/%s/.git", EXTENSIONPATH, ext.Name())
		if _, err := os.Stat(gitPath); err == nil {
			validExtensions = append(validExtensions, ext.Name())
		}
	}

	return validExtensions
}

// get all of the valid skins - in order to be valid, it must exist in the skin path, and be
// a git repository
func GetValidSkins() []string {
	var validSkins []string
	entries, err := os.ReadDir(SKINPATH)
	if err != nil {
		log.Fatal(err)
	}

	for _, skin := range entries {
		if !skin.IsDir() {
			continue
		}

		gitPath := fmt.Sprintf("%s/%s/.git", SKINPATH, skin.Name())
		if _, err := os.Stat(gitPath); err == nil {
			validSkins = append(validSkins, skin.Name())
		}
	}

	return validSkins
}

// execute the deploy
func executeDeploy(config *DeployConfig) error {
	var exitCodes []int

	if isLocalHost(config.Servers) {

		if config.UpgradeVendor {
			fmt.Println("Updating vendor...")
			if err := updateVendor(); err != nil {
				exitCodes = append(exitCodes, 1)
				if !config.Force {
					return err
				}
			}
		}

		for _, ext := range config.UpgradeExtensions {
			fmt.Printf("Updating extension: %s\n", ext)
			if err := updateExtension(ext); err != nil {
				exitCodes = append(exitCodes, 1)
				if !config.Force {
					return err
				}
			}
		}

		for _, skin := range config.UpgradeSkins {
			fmt.Printf("Updating skin: %s\n", skin)
			if err := updateSkin(skin); err != nil {
				exitCodes = append(exitCodes, 1)
				if !config.Force {
					return err
				}
			}
		}

		if err := rsyncToLocalProduction(config); err != nil {
			exitCodes = append(exitCodes, 1)
			if !config.Force {
				return err
			}
		}

		if config.L10n {
			fmt.Println("Rebuilding localization cache...")
			if err := rebuildL10n(config.Lang); err != nil {
				exitCodes = append(exitCodes, 1)
				if !config.Force {
					return err
				}
			}
		}
	}

	for _, server := range config.Servers {
		if server.Name == HOSTNAME {
			continue
		}
		fmt.Printf("Syncing to remote server: %s\n", server.Name)
		if err := rsyncToRemoteServer(server, config); err != nil {
			exitCodes = append(exitCodes, 1)
			if !config.Force {
				return err
			}
		}
	}

	for _, code := range exitCodes {
		if code != 0 {
			return fmt.Errorf("deployment completed with errors")
		}
	}

	return nil
}

// update vendor
func updateVendor() error {
	vendorPath := STAGINGPATH + "/vendor"

	if err := runCommand("git", "-C", vendorPath, "reset", "--hard"); err != nil {
		return fmt.Errorf("failed to reset vendor: %w", err)
	}

	if err := runCommand("git", "-C", vendorPath, "pull", "--recurse-submodules", "origin", "REL1_43", "--quiet"); err != nil {
		return fmt.Errorf("failed to pull vendor: %w", err)
	}

	cmd := exec.Command("composer", "update", "--no-dev", "--quiet")
	cmd.Dir = STAGINGPATH
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("failed to run composer update: %w", err)
	}

	return nil
}

// update extensions
func updateExtension(extension string) error {
	extPath := fmt.Sprintf("%s/%s", EXTENSIONPATH, extension)

	if err := runCommand("git", "-C", extPath, "pull", "--recurse-submodules", "--quiet"); err != nil {
		return fmt.Errorf("failed to update extension %s: %w", extension, err)
	}

	return nil
}

// update skins
func updateSkin(skin string) error {
	skinPath := fmt.Sprintf("%s/%s", SKINPATH, skin)

	if err := runCommand("git", "-C", skinPath, "pull", "--quiet"); err != nil {
		return fmt.Errorf("failed to update skin %s: %w", skin, err)
	}

	return nil
}

// rsync to the production environment on the same server
func rsyncToLocalProduction(config *DeployConfig) error {
	var rsyncArgs []string

	if config.IgnoreTime {
		rsyncArgs = []string{"--inplace"}
	} else {
		rsyncArgs = []string{"--update"}
	}

	if config.UpgradeVendor {
		src := STAGINGPATH + "/vendor/"
		dst := PRODUCTIONPATH + "/vendor/"
		if err := runRsync(rsyncArgs, src, dst); err != nil {
			return err
		}
	}

	for _, ext := range config.UpgradeExtensions {
		src := fmt.Sprintf("%s/%s/", EXTENSIONPATH, ext)
		dst := fmt.Sprintf("%s/extensions/%s/", PRODUCTIONPATH, ext)
		if err := runRsync(rsyncArgs, src, dst); err != nil {
			return err
		}
	}

	for _, skin := range config.UpgradeSkins {
		src := fmt.Sprintf("%s/%s/", SKINPATH, skin)
		dst := fmt.Sprintf("%s/skins/%s/", PRODUCTIONPATH, skin)
		if err := runRsync(rsyncArgs, src, dst); err != nil {
			return err
		}
	}

	return nil
}

// rebuild l10n
func rebuildL10n(lang string) error {
	mergeScript := PRODUCTIONPATH + "/extensions/TelepediaMagic/maintenance/mergeMessageFileList.php"
	cmd := exec.Command("php", RUNNER, mergeScript,
		"--quiet",
		"--wiki="+WIKIDBNAME,
		"--extensions-dir=/prod/mediawiki/extensions:/prod/mediawiki/skins",
		"--output", PRODUCTIONPATH+"/config/ExtensionMessageFiles.php")
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	if err := cmd.Run(); err != nil {
		return fmt.Errorf("failed to merge message files: %w", err)
	}

	rebuildScript := PRODUCTIONPATH + "/maintenance/rebuildLocalisationCache.php"
	args := []string{RUNNER, rebuildScript, "--quiet", "--wiki=" + WIKIDBNAME}

	if lang != "" {
		args = append(args, fmt.Sprintf("--lang=%s", lang))
	}

	cmd = exec.Command("php", args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("failed to rebuild l10n cache: %w", err)
	}

	return nil
}

// rsync the changed files to the other servers
// if we pass --config, we rsync the entire mediawiki install, otherwise, just the specific
// stuff we asked for
func rsyncToRemoteServer(server Server, config *DeployConfig) error {
	sshCmd := "ssh -i /prod/mediawiki-staging/deploykey"

	baseArgs := []string{"-e", sshCmd}

	if config.IgnoreTime {
		baseArgs = append(baseArgs, "--inplace")
	} else {
		baseArgs = append(baseArgs, "--update")
	}

	if config.SyncConfig {
		src := PRODUCTIONPATH + "/"
		dst := fmt.Sprintf("%s@%s:%s/", DEPLOYUSER, server.SSHHost, PRODUCTIONPATH)
		fmt.Printf("  -> [CONFIG] Syncing entire MediaWiki root to %s...\n", server.Name)
		return runRsync(baseArgs, src, dst)
	}

	if config.UpgradeVendor {
		src := PRODUCTIONPATH + "/vendor/"
		dst := fmt.Sprintf("%s@%s:%s/vendor/", DEPLOYUSER, server.SSHHost, PRODUCTIONPATH)
		fmt.Printf("-> Syncing vendor to %s...\n", server.Name)
		if err := runRsync(baseArgs, src, dst); err != nil {
			return err
		}
	}

	for _, ext := range config.UpgradeExtensions {
		src := fmt.Sprintf("%s/extensions/%s/", PRODUCTIONPATH, ext)
		dst := fmt.Sprintf("%s@%s:%s/extensions/%s/", DEPLOYUSER, server.SSHHost, PRODUCTIONPATH, ext)
		fmt.Printf("-> Syncing extension %s to %s...\n", ext, server.Name)
		if err := runRsync(baseArgs, src, dst); err != nil {
			return err
		}
	}

	for _, skin := range config.UpgradeSkins {
		src := fmt.Sprintf("%s/skins/%s/", PRODUCTIONPATH, skin)
		dst := fmt.Sprintf("%s@%s:%s/skins/%s/", DEPLOYUSER, server.SSHHost, PRODUCTIONPATH, skin)
		fmt.Printf("-> Syncing skin %s to %s...\n", skin, server.Name)
		if err := runRsync(baseArgs, src, dst); err != nil {
			return err
		}
	}

	return nil
}

// helper to run a command
func runCommand(name string, args ...string) error {
	cmd := exec.Command(name, args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

// helper to run rsync
func runRsync(baseArgs []string, src, dst string) error {
	args := append(baseArgs, "-r", "--delete", "--exclude=.*", src, dst)

	fmt.Printf("DEBUG: Executing rsync with args: %v\n", args)

	return runCommand("rsync", args...)
}

// short check to see if we currently running on one of the target servers
func isLocalHost(servers []Server) bool {
	for _, s := range servers {
		if s.Name == HOSTNAME {
			return true
		}
	}
	return false
}

// helper to check if a []string array contains a specific item
func contains(slice []string, item string) bool {
	for _, s := range slice {
		if s == item {
			return true
		}
	}
	return false
}
