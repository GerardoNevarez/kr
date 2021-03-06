package main

import (
	"bytes"
	"fmt"
	"io/ioutil"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/urfave/cli"
	. "krypt.co/kr/common/util"
	. "krypt.co/kr/common/analytics"
	. "krypt.co/kr/daemon/client"
)

func globalGitUserIDOrFatal() string {
	userID, err := GlobalGitUserId()
	if err != nil {
		PrintFatal(os.Stderr, Red("Your git name and email are not yet configured. Please run "+
			Cyan("git config --global user.name \"FirstName LastName\"")+
			" and "+
			Cyan("git config --global user.email Email")+
			" before running "+
			Cyan("kr codesign")))
	}
	if err != nil {
		PrintFatal(os.Stderr, err.Error())
	}
	return userID
}

func codesignCommand(c *cli.Context) (err error) {
	stderr := os.Stderr
	latestKrdRunning, err := IsLatestKrdRunning()
	if err != nil || !latestKrdRunning {
		PrintFatal(stderr, ErrOldKrdRunning.Error())
	}
	interactive := c.Bool("interactive")

	checkGitLocation()

	go func() {
		Analytics{}.PostEventUsingPersistedTrackingID("kr", "codesign", nil, nil)
	}()

	userID := globalGitUserIDOrFatal()

	//	explicitly ask phone, disregarding cached ME in case the phone did not support PGP when first paired
	me, err := RequestMeForceRefresh(&userID)
	if err != nil {
		PrintFatal(stderr, err.Error())
	}

	pk, err := me.AsciiArmorPGPPublicKey()
	if err != nil {
		PrintFatal(stderr, "You do not yet have a PGP public key. Make sure you have the latest version of the Krypton app and try again.")
	}

	whichKrGPG, err := exec.Command("which", "krgpg").Output()
	if err != nil {
		PrintFatal(stderr, "Could not find krgpg: "+err.Error())
	}
	krGPGPath := strings.TrimSpace(string(whichKrGPG))

	err = exec.Command("git", "config", "--global", "gpg.program", krGPGPath).Run()
	if err != nil {
		PrintFatal(os.Stderr, err.Error())
	}

	os.Stderr.WriteString("Code signing uses a different type of public key than SSH, called a " + Cyan("PGP public key") + "\r\n")

	onboardGithub(pk)

	os.Stderr.WriteString("You can print this key in the future by running " + Cyan("kr me pgp") + " or copy it to your clipboard by running " + Cyan("kr copy pgp") + "\r\n\r\n")

	onboardAutoCommitSign(interactive)

	onboardLocalGPG(interactive, me)

	onboardGPG_TTY(interactive)

	return
}

func runCommandWithOutputOrFatal(cmd *exec.Cmd) {
	out, err := cmd.CombinedOutput()
	if err != nil {
		PrintFatal(os.Stderr, "error runing command: "+err.Error()+"\r\n"+string(out))
	} else {
		PrintErr(os.Stderr, strings.TrimSpace(string(out)))
	}
}

func codesignOnCommand(c *cli.Context) (err error) {
	exec.Command("git", "config", "--global", "commit.gpgSign", "true").Run()
	PrintErr(os.Stderr, "Automatic commit signing enabled. Disable by running "+Cyan("kr codesign off"))
	return
}

func codesignOffCommand(c *cli.Context) (err error) {
	exec.Command("git", "config", "--global", "--unset", "commit.gpgSign").Run()
	PrintErr(os.Stderr, "Automatic commit signing disabled. Sign a new commit by running "+Cyan("git commit -S")+" or sign your last commit by running "+Cyan("git commit --amend -S")+"\r\nRe-enable automatic commit signing by running "+Cyan("kr codesign on"))
	return
}

func codesignTestCommand(c *cli.Context) (err error) {
	dir, err := ioutil.TempDir("", "kr-git-test")
	if err != nil {
		PrintFatal(os.Stderr, err.Error())
	}
	defer os.RemoveAll(dir)

	os.Setenv("GIT_DIR", dir+"/repo/.git")
	os.Setenv("GIT_WORK_DIR", dir+"/repo")
	runCommandWithOutputOrFatal(exec.Command("git", "init", dir+"/repo"))
	runCommandWithOutputOrFatal(exec.Command("git", "commit", "-S", "--allow-empty", "-m", "Testing your first signed commit"))
	PrintErr(os.Stderr, Green("Krypton ▶ Codesigning successful ✔"))
	return
}

func codesignUninstallCommand(c *cli.Context) (err error) {
	uninstallCodesigning()
	os.Stderr.WriteString("Krypton codesigning uninstalled... run " + Cyan("kr codesign") + " to reinstall.\r\n")
	return
}

func onboardGithub(pk string) {
	if confirm(os.Stderr, "Would you like to add this key to "+Cyan("GitHub")+"?") {
		copyPGPKey()
		<-time.After(1000 * time.Millisecond)
		os.Stderr.WriteString("Press " + Cyan("ENTER") + " to open your browser to GitHub settings. Then click " + Cyan("New GPG key") + " and paste your Krypton PGP public key.\r\n")
		os.Stdin.Read([]byte{0})
		openBrowser("https://github.com/settings/keys")
	}
}

func onboardAutoCommitSign(interactive bool) {
	var autoSign bool
	if interactive {
		if confirm(os.Stderr, "Would you like to enable "+Cyan("automatic commit signing")+"?") {
			autoSign = true
		}
	}
	if autoSign || !interactive {
		err := exec.Command("git", "config", "--global", "commit.gpgSign", "true").Run()
		if err != nil {
			PrintErr(os.Stderr, err.Error()+"\r\n")
		}
		err = exec.Command("git", "config", "--global", "tag.forceSignAnnotated", "true").Run()
		if err != nil {
			PrintErr(os.Stderr, err.Error()+"\r\n")
		}

		os.Stderr.WriteString(Green("Automatic commit signing enabled ✔ ") + " disable by running " + Cyan("git config --global --unset commit.gpgSign") + "\r\n")
	} else {
		os.Stderr.WriteString("You can manually create a signed git commit by running " + Cyan("git commit -S") + "\r\n")
	}
	<-time.After(500 * time.Millisecond)
}

func shellRCFileAndGPG_TTYExport() (file string, export string) {
	exists := func(file string) bool {
		_, err := os.Stat(file)
		return err == nil
	}
	shell := os.Getenv("SHELL")
	home := os.Getenv("HOME")

	zshrc := filepath.Join(home, ".zshrc")
	bashProfile := filepath.Join(home, ".bash_profile")
	bashRc := filepath.Join(home, ".bashrc")
	bashLogin := filepath.Join(home, ".bash_login")
	profile := filepath.Join(home, ".profile")

	kshRc := filepath.Join(home, ".kshrc")
	cshRc := filepath.Join(home, ".cshrc")
	fishConfig := filepath.Join(home, ".config", "fish", "config.fish")
	if strings.Contains(shell, "zsh") {
		return zshrc, "export GPG_TTY=$(tty)"
	} else if strings.Contains(shell, "bash") && exists(bashProfile) {
		return bashProfile, "export GPG_TTY=$(tty)"
	} else if strings.Contains(shell, "bash") && exists(bashLogin) {
		return bashLogin, "export GPG_TTY=$(tty)"
	} else if strings.Contains(shell, "bash") && exists(bashRc) {
		return bashRc, "export GPG_TTY=$(tty)"
	} else if strings.Contains(shell, "ksh") {
		return kshRc, "export GPG_TTY=$(tty)"
	} else if strings.Contains(shell, "csh") {
		return cshRc, "setenv GPG_TTY `tty`"
	} else if strings.Contains(shell, "fish") {
		return fishConfig, "set -x GPG_TTY (tty)"
	} else {
		return profile, "export GPG_TTY=$(tty)"
	}
}

func addGPG_TTYExportToCurrentShellIfNotPresent() (path, cmd string) {
	path, cmd = shellRCFileAndGPG_TTYExport()
	rcContents, err := ioutil.ReadFile(path)
	if err == nil {
		if strings.Contains(string(rcContents), cmd) {
			return
		}
	}
	rcFile, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0755)
	if err != nil {
		return
	}
	//	seek to end
	rcFile.Seek(0, 2)
	rcFile.WriteString("\n# Added by Krypton\n" + cmd + "\n")
	rcFile.Close()
	return
}

func onboardGPG_TTY(interactive bool) {
	cmd := "export GPG_TTY=$(tty); "
	if os.Getenv("GPG_TTY") == "" {
		if interactive {
			os.Stderr.WriteString("\r\n" + Red("WARNING:") + " In order to see Krypton log messages when requesting a git signature, add " + Yellow("export GPG_TTY=$(tty)") + " to your shell startup (~/.bash_profile, ~/.zshrc, etc.) and restart your terminal.\r\n")
			os.Stderr.WriteString("Press " + Cyan("ENTER") + " to continue")
			os.Stdin.Read([]byte{0})
			os.Stderr.WriteString("\r\n")
		} else {
			_, cmd = addGPG_TTYExportToCurrentShellIfNotPresent()
			cmd += "; "
		}
	} else {
		cmd = ""
	}
	os.Stderr.WriteString("\r\nIn order to make sure everything works,\r\n" + Yellow("RUN: ") +
		Red(fmt.Sprintf("%skr codesign test", cmd)) +
		"\r\n\r\n")
}

func onboardKeyServerUpload(interactive bool, pk string) {
	var uploadKey bool
	if interactive {
		if confirm(os.Stderr, "In order for other people to verify your commits, they need to be able to download your public key. Would you like to "+Cyan("upload your public key to the MIT keyserver")+"?") {
			uploadKey = true
		}
	}
	if uploadKey || !interactive {
		cmd := exec.Command("curl", "https://pgp.mit.edu/pks/add", "-f", "--data-urlencode", "keytext="+pk)
		output, err := cmd.CombinedOutput()
		if err == nil {
			os.Stderr.WriteString(Green("Key uploaded ✔\r\n"))
		} else {
			os.Stderr.WriteString(Red("Failed to upload key, curl output:\r\n" + string(output) + "\r\n"))
		}
	}
}

func onboardLocalGPG(interactive bool, me Profile) {
	if !HasGPG() {
		return
	}
	var importKey bool
	if interactive {
		if confirm(os.Stderr, "In order to verify your own commits, you must add your key to gpg locally. Would you like to "+Cyan("add your public key to gpg")+"?") {
			importKey = true
		}
	}
	if importKey || !interactive {
		pkFp, err := me.PGPPublicKeySHA1Fingerprint()
		if err != nil {
			os.Stderr.WriteString(Red("Failed to create key fingerprint:\r\n" + err.Error() + "\r\n"))
			return
		}
		pk, _ := me.AsciiArmorPGPPublicKey()

		cmdImport := exec.Command("gpg", "--import", "--armor")
		cmdImport.Stdin = bytes.NewReader([]byte(pk))
		importOutput, err := cmdImport.CombinedOutput()
		if err != nil {
			os.Stderr.WriteString(Red("Failed to import key, gpg output:\r\n" + string(importOutput) + "\r\n"))
			return
		}

		cmdTrust := exec.Command("gpg", "--import-ownertrust")
		cmdTrust.Stdin = bytes.NewReader([]byte(pkFp + ":6:\r\n"))
		output, err := cmdTrust.CombinedOutput()
		if err == nil {
			os.Stderr.WriteString(Green("Key imported to local gpg keychain ✔\r\n"))
		} else {
			os.Stderr.WriteString(Red("Failed to import key, gpg output:\r\n" + string(output) + "\r\n"))
		}
	}
}

func checkGitLocation() {
	//      make sure git is linked to /usr/bin or /usr/local/bin
	usrBinGitErr := exec.Command("/usr/bin/git", "--version").Run()
	usrLocalBinGitErr := exec.Command("/usr/local/bin/git", "--version").Run()

	if usrBinGitErr == nil || usrLocalBinGitErr == nil {
		return
	}

	gitLocation, err := exec.Command("which", "git").Output()
	if err != nil {
		PrintFatal(os.Stderr, "`which git` failed, please make sure you have git installed and on your PATH")
	}
	gitLocationStr := strings.TrimSpace(string(gitLocation))

	PrintErr(os.Stderr, "git must be linked to /usr/bin or /usr/local/bin to work with Krypton (current location "+gitLocationStr+")")
	if confirm(os.Stderr, "Link git to /usr/local/bin?") {

		linkGitCmd := exec.Command("ln", "-s", gitLocationStr, "/usr/local/bin/git")
		linkGitCmd.Stdout = os.Stdout
		linkGitCmd.Stderr = os.Stderr
		linkGitCmd.Stdin = os.Stdin
		linkGitCmd.Run()
	}
}

func uninstallCodesigning() {
	currentGPGProgram, err := exec.Command("git", "config", "--global", "gpg.program").Output()
	if err != nil {
		return
	}
	if !strings.Contains(string(currentGPGProgram), "krgpg") {
		return
	}
	exec.Command("git", "config", "--global", "--unset", "gpg.program").Run()
	exec.Command("git", "config", "--global", "--unset", "commit.gpgSign").Run()
	exec.Command("git", "config", "--global", "--unset", "tag.forceSignAnnotated").Run()
}
