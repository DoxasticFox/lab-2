
package viewservice

import "net"
import "net/rpc"
import "log"
import "time"
import "sync"
import "fmt"
import "os"

type ViewServer struct {
  mu sync.Mutex
  l net.Listener
  dead bool
  me string


  // Your declarations here.
  view *View
  ACKed bool
  lastView *View
  lastPing map[string]*time.Time
  firstPingDone bool
}

func (vs *ViewServer) isDead (server string) bool {
  if server == "" { return false }  // Server still waiting for first Primary
  
  t := *vs.lastPing[server]
  deadline := t.Add(PingInterval*DeadPings) // possible nil pointer...
  return time.Now().After(deadline)
}

//
// server Ping RPC handler.
//
func (vs *ViewServer) Ping(args *PingArgs, reply *PingReply) error {
  // take a "snapshot" of the view before proceeding
  vs.lastView = &View{
    Viewnum: vs.view.Viewnum,
    Primary: vs.view.Primary,
    Backup:  vs.view.Backup}
  // update the ping time of the calling server
  t := time.Now()
  vs.lastPing[args.Me] = &t
  
  // Is this the ViewServer's first ping?
  if !vs.firstPingDone {
    vs.view.Primary = args.Me
    vs.firstPingDone = true
    goto REPLY_AND_RETURN
  }

  // Primary acknowledged the view sent in previous reply?
  if args.Me == vs.view.Primary && args.Viewnum == vs.view.Viewnum {
    vs.ACKed = true
  }
  // Can we update the view (or just cancel that godawful show altogether)?
  if !vs.ACKed {
    goto REPLY_AND_RETURN // can't update :(
  }

  /*** IT'S HAPPENING: Starting to update the view ***/
  // Remove dead servers
  if vs.isDead(vs.view.Primary) ||
      args.Me == vs.view.Primary && args.Viewnum == 0 {
    vs.view.Primary = ""
  }
  if vs.isDead(vs.view.Backup) ||
      args.Me == vs.view.Backup && args.Viewnum == 0 {
    vs.view.Backup = ""
  }
  if vs.view.Backup == "" && vs.view.Primary == "" {
    goto REPLY_AND_RETURN // (Sort of) fatal: unable to update view again
  }
  // Promote whichever servers we can
  if vs.view.Primary == "" && vs.view.Backup != "" {
    vs.view.Primary = vs.view.Backup
    vs.view.Backup = ""
  }
  if vs.view.Backup == "" &&
      args.Me != vs.view.Primary && args.Me != vs.view.Backup {
    vs.view.Backup = args.Me
  }
  /*** IT'S OGRE NOW: done updating view ***/

  REPLY_AND_RETURN:
  // Has the view changed?
  if vs.lastView.Primary != vs.view.Primary ||
      vs.lastView.Backup != vs.view.Backup {
    vs.view.Viewnum++
    vs.ACKed = false
  }
  // clone vs.view so that Ping()'s callers can't change it
  reply.View = View{
    Viewnum: vs.view.Viewnum,
    Primary: vs.view.Primary,
    Backup:  vs.view.Backup}
  return nil
}

// 
// server Get() RPC handler.
//
func (vs *ViewServer) Get(args *GetArgs, reply *GetReply) error {
  // clone vs.view so that Get()'s callers can't change it
  reply.View = View{
    Viewnum: vs.view.Viewnum,
    Primary: vs.view.Primary,
    Backup:  vs.view.Backup}

  return nil
}


//
// tick() is called once per PingInterval; it should notice
// if servers have died or recovered, and change the view
// accordingly.
//
func (vs *ViewServer) tick() {
  // Ping() took on tick()'s role
}

//
// tell the server to shut itself down.
// for testing.
// please don't change this function.
//
func (vs *ViewServer) Kill() {
  vs.dead = true
  vs.l.Close()
}

func StartServer(me string) *ViewServer {
  vs := new(ViewServer)
  vs.me = me
  // Your vs.* initializations here.
  vs.lastPing = make(map[string]*time.Time)
  vs.view = &View{
    Viewnum: 0,
    Primary: "",
    Backup:  ""}

  // tell net/rpc about our RPC server and handlers.
  rpcs := rpc.NewServer()
  rpcs.Register(vs)

  // prepare to receive connections from clients.
  // change "unix" to "tcp" to use over a network.
  os.Remove(vs.me) // only needed for "unix"
  l, e := net.Listen("unix", vs.me);
  if e != nil {
    log.Fatal("listen error: ", e);
  }
  vs.l = l

  // please don't change any of the following code,
  // or do anything to subvert it.

  // create a thread to accept RPC connections from clients.
  go func() {
    for vs.dead == false {
      conn, err := vs.l.Accept()
      if err == nil && vs.dead == false {
        go rpcs.ServeConn(conn)
      } else if err == nil {
        conn.Close()
      }
      if err != nil && vs.dead == false {
        fmt.Printf("ViewServer(%v) accept: %v\n", me, err.Error())
        vs.Kill()
      }
    }
  }()

  // create a thread to call tick() periodically.
  go func() {
    for vs.dead == false {
      vs.tick()
      time.Sleep(PingInterval)
    }
  }()

  return vs
}
