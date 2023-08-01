package main

import (
	"errors"
	"fmt"
	"github.com/pkg/sftp"
	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/agent"
	"golang.org/x/term"
	"gopkg.in/yaml.v3"
	"io"
	"log"
	"net"
	"os"
	"path/filepath"
	"syscall"
)

const configFileName = "gsend.yaml"

var (
	ErrConfigNotFound = errors.New("config not found")
	ErrSrcNotFound    = errors.New("source file not found")
	ErrDestNotFound   = errors.New("destination not found in config")
	ErrInvalidUsage   = errors.New("invalid usage")
)

type Location struct {
	Path string `yaml:"path"`
	Host string `yaml:"host"`
	Port int    `yaml:"port"`
}

type Config struct {
	Username  string              `yaml:"username"`
	Locations map[string]Location `yaml:"locations"`
}

var (
	src      string
	destName string
	dest     Location
	password string
	size     = 1 << 15
	cfgFile  string
	cfg      Config
)

func writeDefaultConfig() error {
	err := os.MkdirAll(filepath.Dir(cfgFile), syscall.O_RDWR)
	if err != nil {
		return err
	}
	log.Println("writing new config:", cfgFile)
	f, err := os.Create(cfgFile)
	if err != nil {
		return err
	}
	defer f.Close()
	w := yaml.NewEncoder(f)
	defer w.Close()
	return w.Encode(cfg)
}

func printUsage() {
	fmt.Println("USAGE:")
	fmt.Println("gsend <src> <dest>")
}

func getInfo() error {
	cfgBytes, err := os.ReadFile(cfgFile)
	if errors.Is(err, os.ErrNotExist) {
		return errors.Join(ErrConfigNotFound, writeDefaultConfig())
	}
	log.Println("found config:", cfgFile)
	err = yaml.Unmarshal(cfgBytes, &cfg)
	if err != nil {
		return err
	}
	log.Println("config parsed")
	if len(os.Args) < 2 {
		printUsage()
		return ErrInvalidUsage
	}
	src = os.Args[1]
	if _, err = os.Stat(src); errors.Is(err, os.ErrNotExist) {
		return ErrSrcNotFound
	}
	destName = os.Args[2]
	var ok bool
	dest, ok = cfg.Locations[destName]
	if !ok {
		return ErrDestNotFound
	}

	fmt.Println("enter password")
	pwd, err := term.ReadPassword(int(os.Stdin.Fd()))
	if err != nil {
		return err
	}
	log.Println("got password")
	password = string(pwd)
	return nil
}

func init() {
	cfgDir, err := os.UserConfigDir()
	if err != nil {
		panic(err)
	}
	cfgFile = filepath.Join(cfgDir, "gsend", configFileName)
	log.Println("cfg file", cfgFile)
}

func main() {
	err := getInfo()
	if err != nil {
		log.Fatalln(err)
	}
	var auths []ssh.AuthMethod
	if aconn, err := net.Dial("unix", os.Getenv("SSH_AUTH_SOCK")); err == nil {
		auths = append(auths, ssh.PublicKeysCallback(agent.NewClient(aconn).Signers))
	}
	if password != "" {
		auths = append(auths, ssh.Password(password))
	}

	config := ssh.ClientConfig{
		User:            cfg.Username,
		Auth:            auths,
		HostKeyCallback: ssh.InsecureIgnoreHostKey(),
	}
	addr := fmt.Sprintf("%s:%d", dest.Host, dest.Port)
	log.Println("connecting")
	conn, err := ssh.Dial("tcp", addr, &config)
	if err != nil {
		log.Fatalf("unable to connect to [%s]: %v", addr, err)
	}
	defer conn.Close()

	c, err := sftp.NewClient(conn, sftp.MaxPacket(size))
	if err != nil {
		log.Fatalf("unable to start sftp subsytem: %v", err)
	}
	defer c.Close()

	log.Println("connected")
	log.Println("sending file")
	w, err := c.Create(filepath.Join(dest.Path, filepath.Base(src)))
	if err != nil {
		log.Fatal(err)
	}
	defer w.Close()

	s, _ := os.Open(src)
	n, err := io.Copy(w, s)
	if err != nil {
		log.Fatal(err)
	}
	log.Printf("wrote %v bytes", n)
}
