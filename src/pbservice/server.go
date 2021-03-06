package pbservice

import "net"
import "fmt"
import "net/rpc"
import "log"
import "time"
import "viewservice"
import "os"
import "syscall"
import "math/rand"
import "sync"

import "strconv"

// Debugging
const Debug = 0

func DPrintf(format string, a ...interface{}) (n int, err error) {
  if Debug > 0 {
    n, err = fmt.Printf(format, a...)
  }
  return
}

type PBServer struct {
  l net.Listener
  dead bool // for testing
  unreliable bool // for testing
  me string
  vs *viewservice.Clerk
  done sync.WaitGroup
  finish chan interface{}
  // Your declarations here.
  vshost string
  putCalls map[PutArgs]bool // makeshift `set' of PutArgs
  kvs map[string]string // keys and (possibly hashed) values
  mutex *sync.Mutex
  latestView *viewservice.View
}

func (pb *PBServer) putForward(args *PutArgs, reply *PutReply) error {
  if getBackup(pb.vshost) == "" {     // puts should only be received by backups
    return nil
  }

  args.Commit = true  // immediately commit changes to database. (Assumes backup
                      // will never receive duplicate calls from this function.)
  for ok := false; ; {
    ok = call(getBackup(pb.vshost), "PBServer.Put", args, &reply)
    if ok && reply.Err == "" { break }
    time.Sleep(viewservice.PingInterval)
  }

  return nil
}

func (pb *PBServer) Put(args *PutArgs, reply *PutReply) error {
  pb.mutex.Lock() // for putCalls and kvs
  defer pb.mutex.Unlock()
  
  /* Some initilization */
  var ok bool
  var previous string
  previous, ok = pb.kvs[args.Key]
  if !ok {
    previous = ""
  }

  /* Don't put (return early) if we already received these args. Ensures
   * at-most-once semantics */
  _, ok = pb.putCalls[*args]
  if ok {
    reply.PreviousValue = previous
    return nil
  }
  pb.putCalls[*args] = true//remember these arguments so we don't put them again


  /* Forward to backup */
  if pb.me == pb.latestView.Primary {
    argsClone := &PutArgs{     // putForward will modify args if it's not cloned
        Key:    args.Key,
        Value:  args.Value,
        DoHash: args.DoHash,
        Commit: args.Commit /*Actually, this is overridden by putForward*/}
    var ignoredReply PutReply
    pb.putForward(argsClone, &ignoredReply)
  }

  /* Put (and hash if requested)*/
  if args.Commit {
    if args.DoHash {
      h := hash(previous + args.Value)
      args.Value = strconv.Itoa(int(h))
    }
    pb.kvs[args.Key] = args.Value
  }

  /* Finish up */
  reply.Err = ""
  reply.PreviousValue = previous
  return nil
}

func (pb *PBServer) Get(args *GetArgs, reply *GetReply) error {
  v, ok := pb.kvs[args.Key]

  if pb.me != getPrimary(pb.vshost) {       // only the primary can serve get()s
    reply.Err = ErrWrongServer
  } else if !ok {              // Can't find the value corresponding to args.Key
    reply.Err = ErrNoKey
  } else {                                                       // No problemo!
    reply.Value = v
    reply.Err = ""
  }

  return nil
}


// ping the viewserver periodically.
func (pb *PBServer) tick() {
  oldBackup := pb.latestView.Backup // store to compare to more recent view
  /* Ping to get latest view */
  args := &viewservice.PingArgs{pb.me, pb.latestView.Viewnum}
  var reply viewservice.PingReply
  ok := call(pb.vshost, "ViewServer.Ping", args, &reply)
  if !ok {
    return
  }
  pb.latestView = &reply.View

  /* If the backup changed, send a copy of the database */
  if oldBackup == pb.latestView.Backup {
    return // Nothing to do
  }
  for k, v := range pb.kvs {
    args := &PutArgs{
        Key:    k,
        Value:  v,
        DoHash: false,
        Commit: true /*Actually, this is overridden by putForward*/}
    var reply PutReply
    pb.putForward(args, &reply)
  }
}


// tell the server to shut itself down.
// please do not change this function.
func (pb *PBServer) kill() {
  pb.dead = true
  pb.l.Close()
}


func StartServer(vshost string, me string) *PBServer {
  pb := new(PBServer)
  pb.me = me
  pb.vs = viewservice.MakeClerk(me, vshost)
  pb.finish = make(chan interface{})
  // Your pb.* initializations here.
  pb.vshost = vshost
  pb.putCalls = make(map[PutArgs]bool)
  pb.kvs = make(map[string]string)
  pb.latestView = &viewservice.View{
      Viewnum: 0,
      Primary: "",
      Backup:  ""}
  pb.mutex = &sync.Mutex{}

  rpcs := rpc.NewServer()
  rpcs.Register(pb)

  os.Remove(pb.me)
  l, e := net.Listen("unix", pb.me);
  if e != nil {
    log.Fatal("listen error: ", e);
  }
  pb.l = l

  // please do not change any of the following code,
  // or do anything to subvert it.

  go func() {
    for pb.dead == false {
      conn, err := pb.l.Accept()
      if err == nil && pb.dead == false {
        if pb.unreliable && (rand.Int63() % 1000) < 100 {
          // discard the request.
          conn.Close()
        } else if pb.unreliable && (rand.Int63() % 1000) < 200 {
          // process the request but force discard of reply.
          c1 := conn.(*net.UnixConn)
          f, _ := c1.File()
          err := syscall.Shutdown(int(f.Fd()), syscall.SHUT_WR)
          if err != nil {
            fmt.Printf("shutdown: %v\n", err)
          }
          pb.done.Add(1)
          go func() {
            rpcs.ServeConn(conn)
            pb.done.Done()
          }()
        } else {
          pb.done.Add(1)
          go func() {
            rpcs.ServeConn(conn)
            pb.done.Done()
          }()
        }
      } else if err == nil {
        conn.Close()
      }
      if err != nil && pb.dead == false {
        fmt.Printf("PBServer(%v) accept: %v\n", me, err.Error())
        pb.kill()
      }
    }
    DPrintf("%s: wait until all request are done\n", pb.me)
    pb.done.Wait() 
    // If you have an additional thread in your solution, you could
    // have it read to the finish channel to hear when to terminate.
    close(pb.finish)
  }()

  pb.done.Add(1)
  go func() {
    for pb.dead == false {
      pb.tick()
      time.Sleep(viewservice.PingInterval)
    }
    pb.done.Done()
  }()

  return pb
}
