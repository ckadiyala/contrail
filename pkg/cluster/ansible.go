package cluster

import (
	"bytes"
	"io/ioutil"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/flosch/pongo2"
	"github.com/mattn/go-shellwords"
	"github.com/siddontang/go/log"
)

const (
	enable  = "yes"
	disable = "no"
)

type openstackVariables struct {
	enableHaproxy string
}

type ansibleProvisioner struct {
	provisionCommon
}

func (a *ansibleProvisioner) getInstanceTemplate() (instanceTemplate string) {
	return filepath.Join(a.getTemplateRoot(), defaultInstanceTemplate)
}

func (a *ansibleProvisioner) getInstanceFile() (instanceFile string) {
	return filepath.Join(a.getWorkingDir(), defaultInstanceFile)
}

func (a *ansibleProvisioner) getAnsibleRepoDir() (ansibleRepoDir string) {
	return filepath.Join(a.getWorkingDir(), defaultAnsibleRepo)
}

func (a *ansibleProvisioner) cloneAnsibleDeployer() error {
	a.log.Infof("Clean working dir to clone %s", defaultAnsibleRepo)
	repoDir := a.getAnsibleRepoDir()
	err := os.RemoveAll(repoDir)
	if err != nil {
		return err
	}
	a.log.Infof("Cloning repo:%s into %s", defaultAnsibleRepoURL, repoDir)
	args := []string{"clone", defaultAnsibleRepoURL, repoDir}
	err = a.execCmd("git", args, "")
	if err != nil {
		return err
	}
	a.log.Info("Cloning completed")

	return nil
}

func (a *ansibleProvisioner) fetchAnsibleDeployer() error {
	repoDir := a.getAnsibleRepoDir()

	a.log.Infof("Fetching :%s", a.cluster.config.AnsibleFetchURL)
	args, err := shellwords.Parse(a.cluster.config.AnsibleFetchURL)
	if err != nil {
		return err
	}
	args = append([]string{"fetch"}, args...)
	err = a.execCmd("git", args, repoDir)
	if err != nil {
		return err
	}
	a.log.Info("git fetch completed")

	return nil
}

func (a *ansibleProvisioner) cherryPickAnsibleDeployer() error {
	repoDir := a.getAnsibleRepoDir()
	a.log.Infof("Cherry-picking :%s", a.cluster.config.AnsibleCherryPickRevision)
	args := []string{"cherry-pick", a.cluster.config.AnsibleCherryPickRevision}
	err := a.execCmd("git", args, repoDir)
	if err != nil {
		return err
	}
	a.log.Info("Cherry-pick completed")

	return nil
}

func (a *ansibleProvisioner) resetAnsibleDeployer() error {
	repoDir := a.getAnsibleRepoDir()
	a.log.Infof("Git reset to %s", a.cluster.config.AnsibleRevision)
	args := []string{"reset", "--hard", a.cluster.config.AnsibleRevision}
	err := a.execCmd("git", args, repoDir)
	if err != nil {
		return err
	}
	a.log.Info("Git reset completed")

	return nil
}

func (a *ansibleProvisioner) compareInventory() (identical bool, err error) {
	tmpfile, err := ioutil.TempFile("", "instances")
	if err != nil {
		return false, err
	}
	tmpFileName := tmpfile.Name()
	defer func() {
		if err = os.Remove(tmpFileName); err != nil { // nolint:  gas
			log.Errorf("Error while deleting tmpfile: %s", err)
		}
	}()

	a.log.Debugf("Creating temperory inventory %s", tmpFileName)
	err = a.createInstancesFile(tmpFileName)
	if err != nil {
		return false, err
	}

	newInventory, err := ioutil.ReadFile(tmpFileName)
	if err != nil {
		return false, err
	}
	oldInventory, err := ioutil.ReadFile(a.getInstanceFile())
	if err != nil {
		return false, err
	}

	return bytes.Equal(oldInventory, newInventory), nil
}

func (a *ansibleProvisioner) createInventory() error {
	return a.createInstancesFile(a.getInstanceFile())
}

func (a *ansibleProvisioner) getOpenstackDerivedVars() *openstackVariables {
	openstackVars := openstackVariables{}
	cluster := a.clusterData.clusterInfo
	// Enable haproxy when multiple openstack control nodes present in cluster
	if len(cluster.OpenstackControlNodes) > 1 {
		openstackVars.enableHaproxy = enable
		return &openstackVars
	}
	// get openstack control node ip when single node is present
	if cluster.ControlDataNetworkList != "" {
		openstackManagementIP := ""
		for _, node := range a.clusterData.nodesInfo {
			if node.UUID == cluster.OpenstackControlNodes[0].NodeRefs[0].UUID {
				openstackManagementIP = node.IPAddress
				break
			}
		}
		openstackIP := net.ParseIP(openstackManagementIP)
		if openstackIP == nil {
			openstackVars.enableHaproxy = disable
			return &openstackVars
		}
		controlDataNetworks := strings.Split(cluster.ControlDataNetworkList, ",")
		for _, controlDataNetwork := range controlDataNetworks {
			_, network, _ := net.ParseCIDR(controlDataNetwork)
			// do not enable haproxy if openstack ip is in control data net list
			// management network is specifed as control data network as well
			// to make use of the ansible-deployers interface discovery feature.
			if network.Contains(openstackIP) {
				openstackVars.enableHaproxy = disable
				return &openstackVars
			}
		}
		// enable haproxy if openstack ip is not in control data net list
		openstackVars.enableHaproxy = enable
		return &openstackVars
	}
	// do not enable haproxy on single openstack control node and single interface setups
	openstackVars.enableHaproxy = disable
	return &openstackVars
}

func (a *ansibleProvisioner) createInstancesFile(destination string) error {
	a.log.Info("Creating instance.yml input file for ansible deployer")
	context := pongo2.Context{
		"cluster":   a.clusterData.clusterInfo,
		"nodes":     a.clusterData.nodesInfo,
		"openstack": a.getOpenstackDerivedVars(),
	}
	content, err := a.applyTemplate(a.getInstanceTemplate(), context)
	if err != nil {
		return err
	}

	// string empty lines in instances yml content
	regex, _ := regexp.Compile("\n[ \r\n\t]*\n")
	contentString := regex.ReplaceAllString(string(content), "\n")
	content = []byte(contentString)
	err = a.appendToFile(destination, content)
	if err != nil {
		return err
	}
	a.log.Info("Created instance.yml input file for ansible deployer")
	return nil
}

func (a *ansibleProvisioner) playBook() error {
	repoDir := a.getAnsibleRepoDir()
	cmdline := "ansible-playbook"
	args := []string{"-i", "inventory/", "-e",
		"config_file=" + a.getInstanceFile(),
		"-e orchestrator=" + a.clusterData.clusterInfo.Orchestrator}
	if a.cluster.config.AnsibleSudoPass != "" {
		sudoArg := "-e ansible_sudo_pass=" + a.cluster.config.AnsibleSudoPass
		args = append(args, sudoArg)
	}
	args = append(args, defaultInstanceProvPlay)

	a.log.Infof("Playing instance provisioning playbook: %s %s",
		cmdline, strings.Join(args, " "))
	cmd := exec.Command(cmdline, args...)
	cmd.Dir = repoDir
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return err
	}
	if err = cmd.Start(); err != nil {
		return err
	}

	// Report progress log periodically to stdout/db
	go a.reporter.reportLog(stdout)
	go a.reporter.reportLog(stderr)

	if err = cmd.Wait(); err != nil {
		return err
	}
	a.log.Info("Instance provisioning play completed")

	args = args[:len(args)-1]
	args = append(args, defaultInstanceConfPlay)
	a.log.Infof("Playing instance configuration playbook: %s %s",
		cmdline, strings.Join(args, " "))
	cmd = exec.Command(cmdline, args...)
	cmd.Dir = repoDir
	stdout, err = cmd.StdoutPipe()
	if err != nil {
		return err
	}
	stderr, err = cmd.StderrPipe()
	if err != nil {
		return err
	}
	if err = cmd.Start(); err != nil {
		return err
	}

	// Report progress log periodically to stdout/db
	go a.reporter.reportLog(stdout)
	go a.reporter.reportLog(stderr)

	if err = cmd.Wait(); err != nil {
		return err
	}
	a.log.Info("Instance configuration play completed")

	args = args[:len(args)-1]
	args = append(args, defaultClusterProvPlay)
	a.log.Infof("Playing contrail cluster provisioning playbook: %s %s",
		cmdline, strings.Join(args, " "))
	cmd = exec.Command(cmdline, args...)
	cmd.Dir = repoDir
	stdout, err = cmd.StdoutPipe()
	if err != nil {
		return err
	}
	stderr, err = cmd.StderrPipe()
	if err != nil {
		return err
	}
	if err = cmd.Start(); err != nil {
		return err
	}

	// Report progress log periodically to stdout/db
	go a.reporter.reportLog(stdout)
	go a.reporter.reportLog(stderr)

	if err = cmd.Wait(); err != nil {
		return err
	}
	a.log.Info("Instance configuration play completed")
	return nil
}

func (a *ansibleProvisioner) createCluster() error {
	a.log.Infof("Starting %s of contrail cluster: %s", a.action, a.clusterData.clusterInfo.FQName)
	status := map[string]interface{}{statusField: statusCreateProgress}
	a.reporter.reportStatus(status)

	status[statusField] = statusCreateFailed
	err := a.createWorkingDir()
	if err != nil {
		a.reporter.reportStatus(status)
		return err
	}

	if !a.cluster.config.Test {
		err = a.cloneAnsibleDeployer()
		if err != nil {
			a.reporter.reportStatus(status)
			return err
		}
		if a.cluster.config.AnsibleFetchURL != "" {
			err = a.fetchAnsibleDeployer()
			if err != nil {
				a.reporter.reportStatus(status)
				return err
			}
		}
		if a.cluster.config.AnsibleCherryPickRevision != "" {
			err = a.cherryPickAnsibleDeployer()
			if err != nil {
				a.reporter.reportStatus(status)
				return err
			}
		}
		if a.cluster.config.AnsibleRevision != "" {
			err = a.resetAnsibleDeployer()
			if err != nil {
				a.reporter.reportStatus(status)
				return err
			}
		}
	}
	err = a.createInventory()
	if err != nil {
		a.reporter.reportStatus(status)
		return err
	}

	if !a.cluster.config.Test {
		err = a.playBook()
		if err != nil {
			a.reporter.reportStatus(status)
			return err
		}
	}

	status[statusField] = statusCreated
	a.reporter.reportStatus(status)
	return nil
}

func (a *ansibleProvisioner) isUpdated() (updated bool, err error) {
	status := map[string]interface{}{}
	if _, err := os.Stat(a.getInstanceFile()); err == nil {
		ok, err := a.compareInventory()
		if err != nil {
			status[statusField] = statusUpdateFailed
			a.reporter.reportStatus(status)
			return false, err
		}
		if ok {
			a.log.Infof("contrail cluster: %s is already up-to-date", a.clusterData.clusterInfo.FQName)
			return true, nil
		}
	}
	return false, nil
}

func (a *ansibleProvisioner) updateCluster() error {
	a.log.Infof("Starting %s of contrail cluster: %s", a.action, a.clusterData.clusterInfo.FQName)
	status := map[string]interface{}{}
	status[statusField] = statusUpdateProgress
	a.reporter.reportStatus(status)

	err := a.createInventory()
	if err != nil {
		a.reporter.reportStatus(status)
		return err
	}
	if !a.cluster.config.Test {
		err = a.playBook()
		if err != nil {
			status[statusField] = statusUpdateFailed
			a.reporter.reportStatus(status)
			return err
		}
	}

	status[statusField] = statusUpdated
	a.reporter.reportStatus(status)
	return nil
}

func (a *ansibleProvisioner) deleteCluster() error {
	a.log.Infof("Starting %s of contrail cluster: %s", a.action, a.cluster.config.ClusterID)
	return a.deleteWorkingDir()
}

func (a *ansibleProvisioner) provision() error {
	switch a.action {
	case "create":
		if a.isCreated() {
			return nil
		}
		err := a.createCluster()
		if err != nil {
			return err
		}
		return a.createEndpoints()
	case "update":
		updated, err := a.isUpdated()
		if err != nil {
			return err
		}
		if updated {
			return nil
		}
		err = a.updateCluster()
		if err != nil {
			return err
		}
		return a.updateEndpoints()
	case "delete":
		err := a.deleteCluster()
		if err != nil {
			return err
		}
		return a.deleteEndpoints()
	}
	return nil
}
