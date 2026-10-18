package main

import (
 "bufio"
 "crypto/rand"
 "encoding/hex"
 "encoding/json"
 "fmt"
 "io"
 "log"
 "net"
 "net/http"
 "os"
 "os/exec"
 "runtime"
 "strings"
 "sync"
 "time"
)

type Bridge struct {
 mu sync.Mutex
 input io.WriteCloser
 process *exec.Cmd
 pending map[int]chan map[string]any
 next int
 thread string
 busy bool
 listeners map[chan map[string]any]bool
}
func (b *Bridge) rpc(method string, params any)(map[string]any,error){
 b.mu.Lock()
 b.next++; id:=b.next
 ch:=make(chan map[string]any,1);b.pending[id]=ch
 data,_:=json.Marshal(map[string]any{"id":id,"method":method,"params":params})
 _,err:=b.input.Write(append(data,'\n'))
 b.mu.Unlock()
 if err!=nil{return nil,err}
 defer func(){b.mu.Lock();delete(b.pending,id);b.mu.Unlock()}()
 select{
 case msg:=<-ch:
  if e:=msg["error"];e!=nil{return nil,fmt.Errorf("RPC: %v",e)}
  result,_:=msg["result"].(map[string]any);return result,nil
 case <-time.After(30*time.Second):return nil,fmt.Errorf("RPC timeout")
 }
}
func(b *Bridge) events(scanner *bufio.Scanner){
 for scanner.Scan(){
  var msg map[string]any
  if json.Unmarshal(scanner.Bytes(),&msg)!=nil{continue}
  if id,ok:=msg["id"].(float64);ok{
   b.mu.Lock();ch:=b.pending[int(id)];b.mu.Unlock()
   if ch!=nil{select{case ch<-msg:default:}}
   continue
  }
  method,_:=msg["method"].(string)
  b.mu.Lock()
  if method=="turn/completed"||method=="turn/failed"{b.busy=false}
  for ch:=range b.listeners{select{case ch<-msg:default:}}
  b.mu.Unlock()
 }
 b.mu.Lock();b.process=nil;b.thread="";b.busy=false;b.mu.Unlock()
}
func(b *Bridge) start()error{
 b.mu.Lock()
 if b.process!=nil{b.mu.Unlock();return nil}
 cmd:=exec.Command("codex","app-server")
 input,err:=cmd.StdinPipe();if err!=nil{b.mu.Unlock();return err}
 output,err:=cmd.StdoutPipe();if err!=nil{b.mu.Unlock();return err}
 cmd.Stderr=os.Stderr
 if err=cmd.Start();err!=nil{b.mu.Unlock();return err}
 b.input=input;b.process=cmd;b.mu.Unlock()
 scanner:=bufio.NewScanner(output);scanner.Buffer(make([]byte,4096),4*1024*1024)
 go b.events(scanner)
 _,err=b.rpc("initialize",map[string]any{"clientInfo":map[string]any{"name":"zupfnoter_poc","title":"Zupfnoter POC","version":"0.1.0"},"capabilities":map[string]any{}})
 if err!=nil{return err}
 b.mu.Lock();_,err=b.input.Write([]byte("{\"method\":\"initialized\"}\n"));b.mu.Unlock()
 return err
}
func(b *Bridge) prompt(text string)error{
 b.mu.Lock()
 if b.process==nil||b.busy{b.mu.Unlock();return fmt.Errorf("Codex is not ready")}
 b.busy=true;thread:=b.thread;b.mu.Unlock()
 success:=false
 defer func(){if !success{b.mu.Lock();b.busy=false;b.mu.Unlock()}}()
 if thread==""{
  result,err:=b.rpc("thread/start",map[string]any{"sandbox":"read-only","approvalPolicy":"never","ephemeral":true})
  if err!=nil{return err}
  t,_:=result["thread"].(map[string]any);thread,_=t["id"].(string)
  if thread==""{return fmt.Errorf("missing thread ID")}
  b.mu.Lock();b.thread=thread;b.mu.Unlock()
 }
 _,err:=b.rpc("turn/start",map[string]any{"threadId":thread,"input":[]map[string]string{{"type":"text","text":text}}})
 success=err==nil
 return err
}
func main(){
 secret:=make([]byte,32)
 if _,err:=rand.Read(secret);err!=nil{log.Fatal(err)}
 token:=hex.EncodeToString(secret)
 b:=&Bridge{pending:map[int]chan map[string]any{},listeners:map[chan map[string]any]bool{}}
 mux:=http.NewServeMux()
 mux.HandleFunc("/",func(w http.ResponseWriter,r *http.Request){
  if r.URL.Path!="/"{http.NotFound(w,r);return}
  http.ServeFile(w,r,"web/index.html")
 })
 auth:=func(h http.HandlerFunc)http.HandlerFunc{return func(w http.ResponseWriter,r *http.Request){
  if r.Header.Get("X-Assistant-Token")!=token{http.Error(w,"Unauthorized",401);return}
  if origin:=r.Header.Get("Origin");origin!=""&&origin!="http://"+r.Host{http.Error(w,"Origin denied",403);return}
  w.Header().Set("Cache-Control","no-store");h(w,r)
 }}
 respond:=func(w http.ResponseWriter,v any){w.Header().Set("Content-Type","application/json");_ = json.NewEncoder(w).Encode(v)}
 mux.HandleFunc("/api/start",auth(func(w http.ResponseWriter,r *http.Request){
  if r.Method!="POST"{http.Error(w,"Method not allowed",405);return}
  if err:=b.start();err!=nil{http.Error(w,err.Error(),503);return}
  respond(w,map[string]any{"ok":true})
 }))
 mux.HandleFunc("/api/prompt",auth(func(w http.ResponseWriter,r *http.Request){
  if r.Method!="POST"{http.Error(w,"Method not allowed",405);return}
  var request struct{Text string `json:"text"`}
  if json.NewDecoder(http.MaxBytesReader(w,r.Body,20000)).Decode(&request)!=nil||strings.TrimSpace(request.Text)==""{http.Error(w,"Invalid prompt",400);return}
  if err:=b.prompt(request.Text);err!=nil{http.Error(w,err.Error(),409);return}
  respond(w,map[string]any{"ok":true})
 }))
 mux.HandleFunc("/api/events",auth(func(w http.ResponseWriter,r *http.Request){
  flusher,ok:=w.(http.Flusher);if !ok{http.Error(w,"Streaming unavailable",500);return}
  w.Header().Set("Content-Type","text/event-stream")
  ch:=make(chan map[string]any,32)
  b.mu.Lock();b.listeners[ch]=true;b.mu.Unlock()
  defer func(){b.mu.Lock();delete(b.listeners,ch);b.mu.Unlock()}()
  fmt.Fprint(w,": connected\n\n");flusher.Flush()
  for{select{
   case msg:=<-ch:payload,_:=json.Marshal(msg);fmt.Fprintf(w,"data: %s\n\n",payload);flusher.Flush()
   case <-r.Context().Done():return
  }}
 }))
 listener,err:=net.Listen("tcp","127.0.0.1:4317")
 if err!=nil{log.Fatal(err)}
 url:="http://127.0.0.1:4317/?token="+token
 fmt.Println("Open:",url)
 var command *exec.Cmd
 switch runtime.GOOS{
 case "darwin":command=exec.Command("open",url)
 case "windows":command=exec.Command("rundll32","url.dll,FileProtocolHandler",url)
 default:command=exec.Command("xdg-open",url)
 }
 _=command.Start()
 log.Fatal(http.Serve(listener,mux))
}
