# shelf

`shelf` is a personal tool to put static files on the web. The Owner uploads a file or a folder from a CLI, and the tool serves it at a path on `files.ajitha.fyi`.

## Language

### People

**Owner**:
The one person who can upload, manage, and see all Sites. Nobody can sign up, so nobody else can become an Owner.
_Avoid_: admin, user, account

**Visitor**:
Any person who opens the URL of a Site. A person who logs in but is not the Owner is still a Visitor.
_Avoid_: viewer, guest, user

### Sites

**Site**:
One file or one folder that the Owner uploads to one path, for example `/test`.
_Avoid_: page, project, website

**Visibility**:
Who can see a Site. A Site is **Public** (anyone can see it, and it is on the Index page) or **Private** (only the Owner can see it). A new Site is Private.
_Avoid_: access level, published, unpublished

**Archived**:
A Site that is offline for Visitors. The signed-in Owner can still open it. It keeps its path, files, Visibility, and views. Unarchive puts it back online as it was before. A Private Site is different: it is online, but only the Owner can see it.
_Avoid_: hidden, disabled, inactive

**Index page**:
The page at `files.ajitha.fyi/`. It lists all Public Sites. The signed-in Owner also sees the Private Sites. Archived Sites are not on it. It is not the `index.html` file of a folder Site.
_Avoid_: home page, directory, listing

### Tools

**Admin page**:
The website where the Owner sees and manages all Sites.
_Avoid_: dashboard, console, admin panel

**Upload check**:
A test that the CLI runs on the files before an upload. A failed check shows a warning, and the Owner decides to continue or stop.
_Avoid_: lint, validation
