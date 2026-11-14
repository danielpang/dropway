// SPDX-License-Identifier: FSL-1.1-Apache-2.0

/** Executed only by /usr/bin/python3 inside a disposable Sprite. Never eval locally. */
export const SPRITE_WORKSPACE = "/home/sprite/dropway-workspace";
export const SPRITE_GUEST_HELPER = String.raw`
import base64, hashlib, json, os, stat, subprocess, sys
ROOT='/home/sprite/dropway-workspace'
MAX_FILE=2097152
MAX_TOTAL=10485760
MAX_FILES=500
MAX_REQUEST=16777216

def reject():
    raise ValueError('workspace operation rejected')

def parts(path):
    if not isinstance(path,str) or not path or len(path.encode())>512 or path.startswith('/') or '\\' in path or any(ord(c)<32 or ord(c)==127 for c in path): reject()
    names=path.split('/')
    if any(p in ('','.','..','.git','.ssh','.aws','.npmrc','.netrc') or p.startswith('.env') for p in names): reject()
    return names

def rootfd():
    return os.open(ROOT,os.O_RDONLY|os.O_DIRECTORY|os.O_NOFOLLOW)

def parent(path,create=False):
    names=parts(path); fd=rootfd()
    try:
        for name in names[:-1]:
            if create:
                try: os.mkdir(name,0o700,dir_fd=fd)
                except FileExistsError: pass
            nxt=os.open(name,os.O_RDONLY|os.O_DIRECTORY|os.O_NOFOLLOW,dir_fd=fd)
            os.close(fd); fd=nxt
        return fd,names[-1]
    except:
        os.close(fd); raise

def read(path):
    fd,name=parent(path)
    try:
        f=os.open(name,os.O_RDONLY|os.O_NOFOLLOW|os.O_NONBLOCK,dir_fd=fd)
        try:
            info=os.fstat(f)
            if not stat.S_ISREG(info.st_mode) or info.st_nlink!=1 or info.st_size>MAX_FILE: reject()
            with os.fdopen(f,'rb',closefd=False) as stream: data=stream.read(MAX_FILE+1)
            if len(data)>MAX_FILE: reject()
            return data
        finally: os.close(f)
    finally: os.close(fd)

def write(path,data):
    if len(data)>MAX_FILE: reject()
    fd,name=parent(path,True)
    try:
        # Open without truncation first; refuse links/devices before writing.
        f=os.open(name,os.O_WRONLY|os.O_CREAT|os.O_NOFOLLOW|os.O_NONBLOCK,0o600,dir_fd=fd)
        try:
            info=os.fstat(f)
            if not stat.S_ISREG(info.st_mode) or info.st_nlink!=1: reject()
            os.ftruncate(f,0)
            with os.fdopen(f,'wb',closefd=False) as stream: stream.write(data)
        finally: os.close(f)
    finally: os.close(fd)

def encode(path,data):
    return {'path':path,'contentBase64':base64.b64encode(data).decode(),'sha256':hashlib.sha256(data).hexdigest(),'size':len(data)}

def main():
    raw=sys.stdin.buffer.read(MAX_REQUEST+1)
    if len(raw)>MAX_REQUEST: reject()
    request=json.loads(raw)
    action=request['action']; data=request.get('input',{})
    if action=='init':
        os.makedirs(ROOT,mode=0o700,exist_ok=True)
        fd=rootfd(); os.close(fd)
        os.makedirs('/tmp/dropway-home',mode=0o700,exist_ok=True)
        print(json.dumps({'ok':True})); return
    if action=='stage':
        files=data['files']; total=0; seen=set()
        if not isinstance(files,list) or len(files)>MAX_FILES: reject()
        for item in files:
            path=item['path']; parts(path)
            if path in seen: reject()
            seen.add(path)
            blob=base64.b64decode(item['contentBase64'],validate=True); total+=len(blob)
            if total>MAX_TOTAL or len(blob)!=item['size'] or hashlib.sha256(blob).hexdigest()!=item['sha256']: reject()
            write(path,blob)
        print(json.dumps({'ok':True})); return
    if action=='read':
        print(json.dumps({'text':read(data['path']).decode('utf-8')})); return
    if action=='write':
        write(data['path'],data['content'].encode('utf-8'))
        print(json.dumps({'text':'Draft file written.'})); return
    if action=='edit':
        text=read(data['path']).decode('utf-8'); old=data['oldText']
        if not old or text.count(old)!=1: reject()
        write(data['path'],text.replace(old,data['newText'],1).encode('utf-8'))
        print(json.dumps({'text':'Draft file edited.'})); return
    if action in ('bash','build'):
        command=data['command']
        if not isinstance(command,str) or not command or len(command.encode())>8192: reject()
        # Clean guest environment, never the host worker's process.env.
        env={'PATH':'/usr/local/bin:/usr/bin:/bin','HOME':'/tmp/dropway-home','TMPDIR':'/tmp','LANG':'C.UTF-8','CI':'1'}
        os.chdir(ROOT)
        os.execve('/bin/bash',['bash','--noprofile','--norc','-c',command],env)
    if action=='export':
        directory=data.get('directory','.')
        if directory!='.': parts(directory)
        start=ROOT if directory=='.' else ROOT+'/'+directory
        fd=rootfd() if directory=='.' else None
        if fd is not None: os.close(fd)
        # Every walked directory/file is checked again with no-follow openat.
        if os.path.islink(start) or not os.path.isdir(start): reject()
        files=[];total=0
        for root,dirs,names in os.walk(start,followlinks=False):
            for name in dirs:
                if os.path.islink(os.path.join(root,name)): reject()
            for name in names:
                full=os.path.join(root,name)
                rooted=os.path.relpath(full,ROOT)
                content=read(rooted); total+=len(content)
                relative=os.path.relpath(full,start); parts(relative)
                files.append(encode(relative,content))
                if len(files)>MAX_FILES or total>MAX_TOTAL: reject()
        files.sort(key=lambda f:f['path'])
        digest=hashlib.sha256(''.join(f['sha256']+'  '+f['path']+'\n' for f in files).encode()).hexdigest()
        print(json.dumps({'files':files,'contentHash':digest})); return
    reject()
try:
    main()
except Exception:
    # Never echo injected payloads or environment in error diagnostics.
    print('workspace operation rejected',file=sys.stderr)
    sys.exit(2)
`;
