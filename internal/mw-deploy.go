package internal

import (
	"flag"
	"fmt"
	"log"
	"os"
	"os/exec"
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

// valid extensions that this script can work on - a extension must exist and have a .git folder to be valid
var VALIDEXTENSIONS []string

// valid skins that this script can work on - a skin must exist and have a .git folder to be valid
var VALIDSKINS []string

// all of the servers that are valid
var ALLSERVERS = []string{"mw1", "mw2", "mwtask1"}

// all possible deploy options
type DeployConfig struct {
	UpgradeExtensions []string
	UpgradeSkins      []string
	UpgradeVendor     bool
	UpgradeWorld      bool
	L10n              bool
	Lang              string
	Servers           []string
	IgnoreTime        bool
	Force             bool
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

	// --upgrade-world is a helper to do everything
	if config.UpgradeWorld {
		config.UpgradeExtensions = VALIDEXTENSIONS
		config.UpgradeSkins = VALIDSKINS
		config.UpgradeVendor = true
		config.L10n = true
		config.IgnoreTime = true
	}

	VALIDEXTENSIONS = GetValidExtensions()
	VALIDSKINS = GetValidSkins()

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

	deployCmd.Parse(args)

	config := &DeployConfig{
		UpgradeVendor: *upgradeVendor,
		UpgradeWorld:  *upgradeWorld,
		L10n:          *l10n,
		Lang:          *lang,
		IgnoreTime:    *ignoreTime,
		Force:         *force,
	}

	if *upgradeExtensions != "" {
		config.UpgradeExtensions = strings.Split(*upgradeExtensions, ",")
	}

	if *upgradeSkins != "" {
		config.UpgradeSkins = strings.Split(*upgradeSkins, ",")
	}

	if *servers != "" {
		if *servers == "all" {
			config.Servers = ALLSERVERS
		} else {
			config.Servers = strings.Split(*servers, ",")
		}
	}

	return config
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

	if config.Lang != "" && !config.L10n {
		return fmt.Errorf("--lang requires --l10n flag")
	}

	return nil
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

	if contains(config.Servers, HOSTNAME) {

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
		if server == HOSTNAME {
			continue
		}
		fmt.Printf("Syncing to remote server: %s\n", server)
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
	rsyncParams := "--update"
	if config.IgnoreTime {
		rsyncParams = "--inplace"
	}

	if config.UpgradeVendor {
		src := STAGINGPATH + "/vendor/"
		dst := PRODUCTIONPATH + "/vendor/"
		if err := runRsync(rsyncParams, src, dst); err != nil {
			return err
		}
	}

	for _, ext := range config.UpgradeExtensions {
		src := fmt.Sprintf("%s/%s/", EXTENSIONPATH, ext)
		dst := fmt.Sprintf("%s/extensions/%s/", PRODUCTIONPATH, ext)
		if err := runRsync(rsyncParams, src, dst); err != nil {
			return err
		}
	}

	for _, skin := range config.UpgradeSkins {
		src := fmt.Sprintf("%s/%s/", SKINPATH, skin)
		dst := fmt.Sprintf("%s/skins/%s/", PRODUCTIONPATH, skin)
		if err := runRsync(rsyncParams, src, dst); err != nil {
			return err
		}
	}

	return nil
}

// rebuild l10n
func rebuildL10n(lang string) error {
	mergeScript := PRODUCTIONPATH + "/extensions/TelepediaMagic/maintenance/mergeMessageFileList.php"
	cmd := exec.Command("php", mergeScript,
		"--quiet",
		"--wiki=metawiki",
		"--extensions-dir=/prod/mediawiki/extensions:/prod/mediawiki/skins",
		"--output", PRODUCTIONPATH+"/config/ExtensionMessageFiles.php")

	if err := cmd.Run(); err != nil {
		return fmt.Errorf("failed to merge message files: %w", err)
	}

	rebuildScript := PRODUCTIONPATH + "/maintenance/rebuildLocalisationCache.php"
	args := []string{rebuildScript, "--quiet", "--wiki=metawiki"}

	if lang != "" {
		args = append(args, fmt.Sprintf("--lang=%s", lang))
	}

	cmd = exec.Command("php", args...)
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("failed to rebuild l10n cache: %w", err)
	}

	return nil
}

// rsync the changed files to the other servers
func rsyncToRemoteServer(server string, config *DeployConfig) error {
	rsyncParams := "-e ssh -i /prod/mediawiki-staging/deploykey"

	if config.IgnoreTime {
		rsyncParams = "--inplace " + rsyncParams
	} else {
		rsyncParams = "--update " + rsyncParams
	}

	src := PRODUCTIONPATH + "/"
	dst := fmt.Sprintf("%s@%s:%s/", DEPLOYUSER, server, PRODUCTIONPATH)

	return runRsync(rsyncParams, src, dst)
}

// helper to run a command
func runCommand(name string, args ...string) error {
	cmd := exec.Command(name, args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

// helper to run rsync
func runRsync(params, src, dst string) error {
	args := strings.Split(params, " ")
	args = append(args, "-r", "--delete", "--exclude=.*", src, dst)

	return runCommand("rsync", args...)
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
