package pbservice

import "hash/fnv"
import "viewservice"

const (
  OK = "OK"
  ErrNoKey = "ErrNoKey"
  ErrWrongServer = "ErrWrongServer"
  Primary = "Primary"
  Backup = "Backup"
)
type Err string
type ServerRole string

type PutArgs struct {
  Key string
  Value string
  DoHash bool // For PutHash
  Commit bool // Whether to commit changes to the database

  // Field names must start with capital letters,
  // otherwise RPC will break.
}

type PutReply struct {
  Err Err
  PreviousValue string // For PutHash
}

type GetArgs struct {
  Key string
  // You'll have to add definitions here.
}

type GetReply struct {
  Err Err
  Value string
}


// Your RPC definitions here.

func hash(s string) uint32 {
  h := fnv.New32a()
  h.Write([]byte(s))
  return h.Sum32()
}

//
// Contact the viewserver to find out the address of either the Primary or
// Backup.
//
func getServer (vs string, pb ServerRole) (string, bool) {
  args := &viewservice.GetArgs{}
  var reply viewservice.GetReply
  ok := call(vs, "ViewServer.Get", args, &reply)
  if ok {
    if pb == Primary {
      return reply.View.Primary, true
    }
    if pb == Backup {
      return reply.View.Backup, true
    }
  }
  return "", false
}

func getPrimary (vs string) string {
  p, ok := getServer(vs, Primary)
  if !ok {
    return ""
  }
  return p
}

func getBackup (vs string) string {
  p, ok := getServer(vs, Backup)
  if !ok {
    return ""
  }
  return p
}

