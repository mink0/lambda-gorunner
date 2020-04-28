package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"log"
	"net"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/session"
	"github.com/aws/aws-sdk-go/service/ec2"
	"github.com/pkg/errors"
	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/agent"
)

const (
	defaultTimeout     = "5"
	defaultMaxSessions = "10"
	defaultUsers       = "centos,ec2-user"
	defaultFacts       = `{"kernel": "uname -rs","release": "cat /etc/redhat-release || cat /etc/*-release"}`
)

// ResRow contain the results of running commands listed in Facts
type ResRow struct {
	InstanceId string
	Name       string
	IPs        []string

	Facts map[string]string
}

// Worker is a wrapper for business logic
func Worker() (resTable []ResRow, err error) {
	startTime := time.Now()

	if _, exists := os.LookupEnv("DEBUG"); !exists {
		log.SetOutput(ioutil.Discard)
	}

	sshAuths, err := sshAuthSetup()
	if err != nil {
		return
	}

	facts := getEnv("FACTS", defaultFacts)
	factsToCollect := map[string]string{}
	if err = json.Unmarshal([]byte(facts), &factsToCollect); err != nil {
		return
	}

	instances, err := getInstances()
	if err != nil {
		return
	}

	maxSessions, _ := strconv.Atoi(getEnv("MAX_SESSIONS", defaultMaxSessions))

	fmt.Printf("Collecting facts (%s) for %v instances(s)...\n", facts, len(instances))

	// concurrency control
	limiter := make(chan int, maxSessions)
	var wg sync.WaitGroup

	// dispatch all at once
	for i := range instances {
		wg.Add(1)
		go processFact(i, limiter, factsToCollect, &wg, sshAuths, instances[i])
	}

	wg.Wait()

	endTime := time.Now()
	diff := endTime.Sub(startTime)

	resTable = formatResult(instances, factsToCollect)

	fmt.Printf("\nProcessed %v instance(s) for %v seconds\n", len(instances), diff.Seconds())

	return
}

func processFact(jobID int, limiter chan int, factsToCollect map[string]string, wg *sync.WaitGroup, auths []*ssh.ClientConfig, instance *InstanceInfo) {
	defer wg.Done()
	limiter <- jobID // block the control until some other goroutine reads from this channel

	// mutate instance
	instance.facts, instance.err = GetFacts(instance.addrs, factsToCollect, auths)
	if instance.err != nil {
		log.Println(instance.err)
	}

	<-limiter // just read to unblock the limiter
}

// GetFacts collects facts from the map
func GetFacts(hostAddrs []string, factsToCollect map[string]string, auths []*ssh.ClientConfig) (map[string]string, error) {
	if len(hostAddrs) == 0 {
		return nil, errors.Errorf("No hosts to get facts")
	}

	//TODO:
	// try to implement .Dial() to all hostAddrs in parallel,
	// but be aware of maximum failed attempts
	conStr := ""
	var client *ssh.Client
	for i := 0; i < len(auths) && conStr == ""; i++ {
		auth := auths[i]
		for _, host := range hostAddrs {
			log.Printf("Trying %s@%s... \n", auth.User, host)

			var err error
			if client, err = ssh.Dial("tcp", host+":22", auth); err == nil {
				conStr = auth.User + "@" + host
				break
			}

			log.Println(errors.Wrap(err, "Failed to connect "+auth.User+"@"+host))
		}
	}

	if conStr == "" {
		return nil, errors.Errorf("Can't connect to host with addresses: %v", hostAddrs)
	}

	// no dead connections left on errors
	defer client.Close()

	type remoteCmd struct {
		session *ssh.Session
		stdout  *bytes.Buffer
		stderr  *bytes.Buffer
		cmd     string
		err     error
	}

	commands := map[string]remoteCmd{}

	// Create a command sessions: one session per command
	for name, cmd := range factsToCollect {
		session, err := client.NewSession()
		if err != nil {
			// DANGER: we are running out of resources
			return nil, errors.Wrap(err, "Can't allocate session for "+conStr)
		}

		commands[name] = remoteCmd{
			cmd:     cmd,
			session: session,
			stdout:  &bytes.Buffer{},
			stderr:  &bytes.Buffer{},
		}
		session.Stdout = commands[name].stdout
		session.Stderr = commands[name].stderr

		// start in parallel
		if err := session.Start(cmd); err != nil {
			return nil, errors.Wrap(err, "Can't start command: '"+cmd+"' at "+conStr)
		}
	}

	facts := map[string]string{}

	combErr := errors.Errorf("can't collect all facts for %s", conStr)
	hasErrors := false
	for name, c := range commands {
		if err := c.session.Wait(); err != nil {
			combErr = errors.Wrapf(combErr, "Failed to collect '%s' fact: %s (@err %s)", name, err.Error(), c.stderr)
			hasErrors = true
		} else {
			facts[name] = strings.TrimSpace(c.stdout.String())
		}

		c.session.Close()
	}

	log.Printf("...[%s] found facts: %v", conStr, facts)

	if !hasErrors {
		combErr = nil
	}

	return facts, combErr
}

func sshAuthSetup() ([]*ssh.ClientConfig, error) {
	sshKey := os.Getenv("SSH_KEY")
	sshKeyPath := os.Getenv("SSH_KEY_PATH")
	sshAuthSock := os.Getenv("SSH_AUTH_SOCK")
	timeout, _ := strconv.Atoi(getEnv("TIMEOUT", defaultTimeout))

	if sshKey == "" && sshKeyPath == "" && sshAuthSock == "" {
		return nil, errors.Errorf("You should provide ssh key or launch SSH agent")
	}

	var authMethod ssh.AuthMethod
	if sshKey != "" || sshKeyPath != "" {
		if sshKey == "" {
			f, err := os.Open(sshKeyPath)
			if err != nil {
				return nil, err
			}

			defer f.Close()

			b, err := ioutil.ReadAll(f)
			if err != nil {
				return nil, errors.Wrap(err, "Can't open ssh key file")
			}

			sshKey = string(b)
		}

		key, err := ssh.ParsePrivateKey([]byte(sshKey))
		if err != nil {
			return nil, err
		}

		authMethod = ssh.PublicKeys(key)
	} else {
		agentConn, err := net.Dial("unix", sshAuthSock)
		if err != nil {
			return nil, errors.Wrap(err, "Can't open connection to SSH agent: "+sshAuthSock)
		}

		agentClient := agent.NewClient(agentConn)
		authMethod = ssh.PublicKeysCallback(agentClient.Signers)
	}

	auths := []*ssh.ClientConfig{}

	users := strings.Split(getEnv("USERS", defaultUsers), ",")
	for i := 0; i < len(users); i++ {
		users[i] = strings.TrimSpace(users[i])
	}

	for _, user := range users {
		// safe copy
		config := &ssh.ClientConfig{
			User: user,
			Auth: []ssh.AuthMethod{
				authMethod,
			},
			HostKeyCallback: ssh.InsecureIgnoreHostKey(),
			Timeout:         time.Second * time.Duration(timeout),
		}

		auths = append(auths, config)
	}

	return auths, nil
}

// InstanceInfo conatains host addresses, collected facts and AWS description
type InstanceInfo struct {
	description *ec2.Instance
	addrs       []string
	facts       map[string]string
	err         error
}

// getInstances finds and describes (aws describe) all running instances
func getInstances() ([]*InstanceInfo, error) {
	s := session.Must(session.NewSessionWithOptions(session.Options{
		SharedConfigState: session.SharedConfigEnable,
	}))

	// Create new EC2 client
	ec2Svc := ec2.New(s)

	params := &ec2.DescribeInstancesInput{
		Filters: []*ec2.Filter{
			{
				Name:   aws.String("instance-state-name"),
				Values: []*string{aws.String("running"), aws.String("pending")},
			},
		},
	}

	instances, err := ec2Svc.DescribeInstances(params)
	if err != nil {
		return nil, errors.Wrap(err, "Can't fetch ec2 instances list")
	}

	instancesInfo := []*InstanceInfo{}

	for _, reservation := range instances.Reservations {
		for _, instance := range reservation.Instances {
			iInfo := &InstanceInfo{}

			iInfo.description = instance
			iInfo.addrs = []string{}

			if instance.PrivateIpAddress != nil && *instance.PrivateIpAddress != "" {
				iInfo.addrs = append(iInfo.addrs, *instance.PrivateIpAddress)
			}

			if instance.PublicIpAddress != nil && *instance.PublicIpAddress != "" {
				iInfo.addrs = append(iInfo.addrs, *instance.PublicIpAddress)
			}

			instancesInfo = append(instancesInfo, iInfo)
		}
	}

	log.Printf("AWS: found %v instance(s) in running or pending state...", len(instancesInfo))

	return instancesInfo, nil
}

func formatResult(instances []*InstanceInfo, factsToCollect map[string]string) (resTable []ResRow) {
	for _, inst := range instances {
		row := ResRow{
			Facts: make(map[string]string),
		}

		if inst.description.InstanceId != nil {
			row.InstanceId = *inst.description.InstanceId
		}

		for _, tag := range inst.description.Tags {
			if *tag.Key == "Name" {
				row.Name = *tag.Value
				break
			}
		}

		row.IPs = inst.addrs

		unkRes := ""
		if inst.facts != nil {
			for k := range factsToCollect {
				res := unkRes
				if fact, ok := inst.facts[k]; ok {
					res = fact
				}
				row.Facts[k] = res
			}
		}

		resTable = append(resTable, row)
	}

	return
}
