// Command mesh is the whole agent mesh: control plane, node daemon, and the
// client an agent uses to talk to its peers.
//
// It is written to be driven by an agent, not only a human: every command takes
// --json, every error names the command that fixes it, and `mesh guide` prints
// everything a fresh agent needs to know without reading this source.
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
)

const usage = `mesh -- a named mesh for agents, over an encrypted peer-to-peer link.

  mesh join [--name N]        join the mesh on this machine and start answering
  mesh guide                  everything an agent needs to use the mesh (read this)
  mesh peers                  who else is here
  mesh ask <peer> <question>  ask a peer, wait for its answer
  mesh group                  name a set of peers, then ask them all at once
  mesh send <peer> <message>  tell a peer something, do not wait
  mesh inbox                  what has been said to you
  mesh wait                   block until a peer says something (run in background)
  mesh waiting                questions addressed to you, awaiting your answer
  mesh reply <id> <answer>    answer one of them
  mesh ping <peer>            is a peer reachable, and how
  mesh id                     this node's fingerprint, to read out when joining
  mesh trust                  what each peer is allowed to do here
  mesh allow / deny <peer>    let a peer ask this node to work, or stop it
  mesh block / unblock <peer> refuse a peer entirely
  mesh log                    what peers have asked of you, refusals included
  mesh status                 this node
  mesh doctor                 what is wrong and what to run next
  mesh version                which build this is

  mesh hub [--mesh NAME]      start the control plane (once per mesh)
  mesh invite                 print the string that lets another machine join
  mesh up / mesh down         run or stop this node's daemon by hand
  mesh service install        keep this node running across reboots and crashes
  mesh connect                register the mesh with the agents installed here
  mesh mcp                    serve the mesh as MCP tools over stdio

Run "mesh <command> --help" for a command's options.`

func main() {
	if len(os.Args) < 2 {
		fmt.Println(usage)
		os.Exit(2)
	}
	cmd, args := os.Args[1], os.Args[2:]
	var err error
	switch cmd {
	case "join":
		err = cmdJoin(args)
	case "up":
		err = cmdUp(args)
	case "down":
		err = cmdDown(args)
	case "service":
		err = cmdService(args)
	case "hub":
		err = cmdHub(args)
	case "invite":
		err = cmdInvite(args)
	case "peers":
		err = cmdPeers(args)
	case "ask":
		err = cmdAsk(args)
	case "send", "tell":
		err = cmdSend(args)
	case "inbox":
		err = cmdInbox(args)
	case "wait":
		err = cmdWait(args)
	case "waiting":
		err = cmdWaiting(args)
	case "reply":
		err = cmdReply(args)
	case "ping":
		err = cmdPing(args)
	case "id":
		err = cmdID(args)
	case "trust":
		err = cmdTrust(args)
	case "allow", "deny", "block", "unblock", "verify", "forget":
		err = cmdTrustAction(cmd)(args)
	case "log":
		err = cmdLog(args)
	case "group":
		err = cmdGroup(args)
	case "status":
		err = cmdStatus(args)
	case "doctor":
		err = cmdDoctor(args)
	case "guide":
		err = cmdGuide(args)
	case "install-skill":
		err = cmdInstallSkill(args)
	case "connect":
		err = cmdConnect(args)
	case "mcp":
		err = cmdMCP(args)
	case "version", "--version", "-v":
		err = cmdVersion(args)
	case "-h", "--help", "help":
		fmt.Println(usage)
		return
	default:
		fmt.Fprintf(os.Stderr, "mesh: unknown command %q\n\n%s\n", cmd, usage)
		os.Exit(2)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "mesh: "+err.Error())
		os.Exit(1)
	}
}

func printJSON(v any) error {
	e := json.NewEncoder(os.Stdout)
	e.SetIndent("", "  ")
	return e.Encode(v)
}

func joinArgs(a []string) string { return strings.Join(a, " ") }
