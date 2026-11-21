export namespace extsess {
	
	export class ExternalSession {
	    id: string;
	    source: string;
	    state: string;
	    pid: number;
	    createMs: number;
	    title?: string;
	    actions: string[];
	
	    static createFrom(source: any = {}) {
	        return new ExternalSession(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.source = source["source"];
	        this.state = source["state"];
	        this.pid = source["pid"];
	        this.createMs = source["createMs"];
	        this.title = source["title"];
	        this.actions = source["actions"];
	    }
	}

}

export namespace githubx {
	
	export class PullRequest {
	    number: number;
	    url: string;
	    title: string;
	
	    static createFrom(source: any = {}) {
	        return new PullRequest(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.number = source["number"];
	        this.url = source["url"];
	        this.title = source["title"];
	    }
	}

}

export namespace gitx {
	
	export class Checkpoint {
	    id: string;
	    ref: string;
	    sha: string;
	    label?: string;
	    hasDiff?: boolean;
	    // Go type: time
	    createdAt: any;
	
	    static createFrom(source: any = {}) {
	        return new Checkpoint(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.ref = source["ref"];
	        this.sha = source["sha"];
	        this.label = source["label"];
	        this.hasDiff = source["hasDiff"];
	        this.createdAt = this.convertValues(source["createdAt"], null);
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	export class PruneResult {
	    prunedRefs: number;
	    removedOrphans?: string[];
	
	    static createFrom(source: any = {}) {
	        return new PruneResult(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.prunedRefs = source["prunedRefs"];
	        this.removedOrphans = source["removedOrphans"];
	    }
	}
	export class WorktreeInfo {
	    path: string;
	    branch?: string;
	    head?: string;
	    agent?: string;
	    sizeBytes: number;
	    isMain?: boolean;
	    detached?: boolean;
	    prunable?: boolean;
	    orphan?: boolean;
	    locked?: boolean;
	
	    static createFrom(source: any = {}) {
	        return new WorktreeInfo(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.path = source["path"];
	        this.branch = source["branch"];
	        this.head = source["head"];
	        this.agent = source["agent"];
	        this.sizeBytes = source["sizeBytes"];
	        this.isMain = source["isMain"];
	        this.detached = source["detached"];
	        this.prunable = source["prunable"];
	        this.orphan = source["orphan"];
	        this.locked = source["locked"];
	    }
	}

}

export namespace idecore {
	
	export class BlameLine {
	    line: number;
	    commit: string;
	    short: string;
	    author: string;
	    timestamp: string;
	    summary: string;
	    text: string;
	
	    static createFrom(source: any = {}) {
	        return new BlameLine(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.line = source["line"];
	        this.commit = source["commit"];
	        this.short = source["short"];
	        this.author = source["author"];
	        this.timestamp = source["timestamp"];
	        this.summary = source["summary"];
	        this.text = source["text"];
	    }
	}
	export class CommandOutput {
	    command: string;
	    output: string;
	    exitCode: number;
	    durationMs: number;
	
	    static createFrom(source: any = {}) {
	        return new CommandOutput(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.command = source["command"];
	        this.output = source["output"];
	        this.exitCode = source["exitCode"];
	        this.durationMs = source["durationMs"];
	    }
	}
	export class CommitDetail {
	    hash: string;
	    short: string;
	    author: string;
	    date: string;
	    subject: string;
	    body?: string;
	    files: remote.FileDiff[];
	
	    static createFrom(source: any = {}) {
	        return new CommitDetail(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.hash = source["hash"];
	        this.short = source["short"];
	        this.author = source["author"];
	        this.date = source["date"];
	        this.subject = source["subject"];
	        this.body = source["body"];
	        this.files = this.convertValues(source["files"], remote.FileDiff);
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	export class ConflictBlock {
	    index: number;
	    startLine: number;
	    endLine: number;
	    oursLabel: string;
	    theirsLabel: string;
	    ours: string;
	    base?: string;
	    theirs: string;
	
	    static createFrom(source: any = {}) {
	        return new ConflictBlock(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.index = source["index"];
	        this.startLine = source["startLine"];
	        this.endLine = source["endLine"];
	        this.oursLabel = source["oursLabel"];
	        this.theirsLabel = source["theirsLabel"];
	        this.ours = source["ours"];
	        this.base = source["base"];
	        this.theirs = source["theirs"];
	    }
	}
	export class ConflictedFile {
	    path: string;
	    blocks: ConflictBlock[];
	
	    static createFrom(source: any = {}) {
	        return new ConflictedFile(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.path = source["path"];
	        this.blocks = this.convertValues(source["blocks"], ConflictBlock);
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	export class DesignModeElement {
	    url?: string;
	    selector: string;
	    tag?: string;
	    id?: string;
	    classes?: string[];
	    textSnippet?: string;
	    outerHtml?: string;
	    computedStyles?: Record<string, string>;
	    boundingBox?: Record<string, number>;
	
	    static createFrom(source: any = {}) {
	        return new DesignModeElement(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.url = source["url"];
	        this.selector = source["selector"];
	        this.tag = source["tag"];
	        this.id = source["id"];
	        this.classes = source["classes"];
	        this.textSnippet = source["textSnippet"];
	        this.outerHtml = source["outerHtml"];
	        this.computedStyles = source["computedStyles"];
	        this.boundingBox = source["boundingBox"];
	    }
	}
	export class DesignModePage {
	    index: number;
	    title?: string;
	    url: string;
	
	    static createFrom(source: any = {}) {
	        return new DesignModePage(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.index = source["index"];
	        this.title = source["title"];
	        this.url = source["url"];
	    }
	}
	export class FileCommitEntry {
	    hash: string;
	    short: string;
	    author: string;
	    timestamp: string;
	    subject: string;
	
	    static createFrom(source: any = {}) {
	        return new FileCommitEntry(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.hash = source["hash"];
	        this.short = source["short"];
	        this.author = source["author"];
	        this.timestamp = source["timestamp"];
	        this.subject = source["subject"];
	    }
	}
	export class FileNode {
	    name: string;
	    path: string;
	    relPath: string;
	    isDir: boolean;
	    hasChildren?: boolean;
	    children?: FileNode[];
	
	    static createFrom(source: any = {}) {
	        return new FileNode(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.name = source["name"];
	        this.path = source["path"];
	        this.relPath = source["relPath"];
	        this.isDir = source["isDir"];
	        this.hasChildren = source["hasChildren"];
	        this.children = this.convertValues(source["children"], FileNode);
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	export class GitGraphCommit {
	    hash: string;
	    short: string;
	    parents: string[];
	    author: string;
	    date: string;
	    subject: string;
	    refs?: string[];
	
	    static createFrom(source: any = {}) {
	        return new GitGraphCommit(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.hash = source["hash"];
	        this.short = source["short"];
	        this.parents = source["parents"];
	        this.author = source["author"];
	        this.date = source["date"];
	        this.subject = source["subject"];
	        this.refs = source["refs"];
	    }
	}
	export class GitHubStatusInfo {
	    installed: boolean;
	    loggedIn: boolean;
	    user: string;
	    remote: string;
	    detail: string;
	    mode?: string;
	    source?: string;
	    connectorLabel?: string;
	    tokenMasked?: string;
	    baseUrl?: string;
	
	    static createFrom(source: any = {}) {
	        return new GitHubStatusInfo(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.installed = source["installed"];
	        this.loggedIn = source["loggedIn"];
	        this.user = source["user"];
	        this.remote = source["remote"];
	        this.detail = source["detail"];
	        this.mode = source["mode"];
	        this.source = source["source"];
	        this.connectorLabel = source["connectorLabel"];
	        this.tokenMasked = source["tokenMasked"];
	        this.baseUrl = source["baseUrl"];
	    }
	}
	export class InlineAIStatus {
	    ghostAvailable: boolean;
	    ghostProvider?: string;
	    ghostModel?: string;
	    ghostReason?: string;
	    editAvailable: boolean;
	    editProvider?: string;
	    editModel?: string;
	
	    static createFrom(source: any = {}) {
	        return new InlineAIStatus(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.ghostAvailable = source["ghostAvailable"];
	        this.ghostProvider = source["ghostProvider"];
	        this.ghostModel = source["ghostModel"];
	        this.ghostReason = source["ghostReason"];
	        this.editAvailable = source["editAvailable"];
	        this.editProvider = source["editProvider"];
	        this.editModel = source["editModel"];
	    }
	}
	export class InlineCompleteResponse {
	    completion: string;
	    model?: string;
	    provider?: string;
	    inputTokens?: number;
	    outputTokens?: number;
	
	    static createFrom(source: any = {}) {
	        return new InlineCompleteResponse(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.completion = source["completion"];
	        this.model = source["model"];
	        this.provider = source["provider"];
	        this.inputTokens = source["inputTokens"];
	        this.outputTokens = source["outputTokens"];
	    }
	}
	export class InlineEditResponse {
	    replacement: string;
	    diffLines: remote.DiffLine[];
	    added: number;
	    removed: number;
	    model?: string;
	    provider?: string;
	    inputTokens?: number;
	    outputTokens?: number;
	
	    static createFrom(source: any = {}) {
	        return new InlineEditResponse(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.replacement = source["replacement"];
	        this.diffLines = this.convertValues(source["diffLines"], remote.DiffLine);
	        this.added = source["added"];
	        this.removed = source["removed"];
	        this.model = source["model"];
	        this.provider = source["provider"];
	        this.inputTokens = source["inputTokens"];
	        this.outputTokens = source["outputTokens"];
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	export class LSPHover {
	    contents: string;
	    line?: number;
	    col?: number;
	    endLine?: number;
	    endCol?: number;
	
	    static createFrom(source: any = {}) {
	        return new LSPHover(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.contents = source["contents"];
	        this.line = source["line"];
	        this.col = source["col"];
	        this.endLine = source["endLine"];
	        this.endCol = source["endCol"];
	    }
	}
	export class LSPLocation {
	    relPath: string;
	    absPath: string;
	    line: number;
	    col: number;
	    endLine?: number;
	    endCol?: number;
	
	    static createFrom(source: any = {}) {
	        return new LSPLocation(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.relPath = source["relPath"];
	        this.absPath = source["absPath"];
	        this.line = source["line"];
	        this.col = source["col"];
	        this.endLine = source["endLine"];
	        this.endCol = source["endCol"];
	    }
	}
	export class LSPStatus {
	    available: boolean;
	    running: boolean;
	    server: string;
	    binary?: string;
	    root?: string;
	    error?: string;
	
	    static createFrom(source: any = {}) {
	        return new LSPStatus(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.available = source["available"];
	        this.running = source["running"];
	        this.server = source["server"];
	        this.binary = source["binary"];
	        this.root = source["root"];
	        this.error = source["error"];
	    }
	}
	export class ProblemItem {
	    source: string;
	    severity: string;
	    file: string;
	    line: number;
	    col: number;
	    endLine?: number;
	    endCol?: number;
	    code?: string;
	    message: string;
	
	    static createFrom(source: any = {}) {
	        return new ProblemItem(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.source = source["source"];
	        this.severity = source["severity"];
	        this.file = source["file"];
	        this.line = source["line"];
	        this.col = source["col"];
	        this.endLine = source["endLine"];
	        this.endCol = source["endCol"];
	        this.code = source["code"];
	        this.message = source["message"];
	    }
	}
	export class ProblemsCheckResult {
	    command: string;
	    exitCode: number;
	    durationMs: number;
	    output: string;
	    problems: ProblemItem[];
	
	    static createFrom(source: any = {}) {
	        return new ProblemsCheckResult(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.command = source["command"];
	        this.exitCode = source["exitCode"];
	        this.durationMs = source["durationMs"];
	        this.output = source["output"];
	        this.problems = this.convertValues(source["problems"], ProblemItem);
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	export class ProjectStack {
	    root: string;
	    name: string;
	    stacks: string[];
	    buildTools: string[];
	    testHint: string;
	    summary: string;
	
	    static createFrom(source: any = {}) {
	        return new ProjectStack(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.root = source["root"];
	        this.name = source["name"];
	        this.stacks = source["stacks"];
	        this.buildTools = source["buildTools"];
	        this.testHint = source["testHint"];
	        this.summary = source["summary"];
	    }
	}
	export class SkillInfo {
	    name: string;
	    description: string;
	    source: string;
	    path: string;
	    updatedAt: string;
	    sizeBytes: number;
	    enabled: boolean;
	
	    static createFrom(source: any = {}) {
	        return new SkillInfo(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.name = source["name"];
	        this.description = source["description"];
	        this.source = source["source"];
	        this.path = source["path"];
	        this.updatedAt = source["updatedAt"];
	        this.sizeBytes = source["sizeBytes"];
	        this.enabled = source["enabled"];
	    }
	}
	export class TextSearchMatch {
	    relPath: string;
	    line: number;
	    preview: string;
	
	    static createFrom(source: any = {}) {
	        return new TextSearchMatch(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.relPath = source["relPath"];
	        this.line = source["line"];
	        this.preview = source["preview"];
	    }
	}
	export class WizardInput {
	    teamName: string;
	    chiefRole: string;
	    devCount: number;
	    maxDynamicDevs?: number;
	    defaultModel: string;
	    agentModels?: Record<string, string>;
	    permissionMode?: string;
	    shellUnsandboxed?: boolean;
	    worktrees: boolean;
	    autoCommit: boolean;
	    autoPR: boolean;
	    jiraEnabled: boolean;
	    jiraProject: string;
	    baseBranch: string;
	    homologBranch?: string;
	    maxTokens: number;
	    skillAssignments?: team.SkillAssignment[];
	
	    static createFrom(source: any = {}) {
	        return new WizardInput(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.teamName = source["teamName"];
	        this.chiefRole = source["chiefRole"];
	        this.devCount = source["devCount"];
	        this.maxDynamicDevs = source["maxDynamicDevs"];
	        this.defaultModel = source["defaultModel"];
	        this.agentModels = source["agentModels"];
	        this.permissionMode = source["permissionMode"];
	        this.shellUnsandboxed = source["shellUnsandboxed"];
	        this.worktrees = source["worktrees"];
	        this.autoCommit = source["autoCommit"];
	        this.autoPR = source["autoPR"];
	        this.jiraEnabled = source["jiraEnabled"];
	        this.jiraProject = source["jiraProject"];
	        this.baseBranch = source["baseBranch"];
	        this.homologBranch = source["homologBranch"];
	        this.maxTokens = source["maxTokens"];
	        this.skillAssignments = this.convertValues(source["skillAssignments"], team.SkillAssignment);
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	export class WorkspaceGitRepo {
	    root: string;
	    name: string;
	    relPath: string;
	    branch: string;
	    isRoot: boolean;
	    diffs: remote.FileDiff[];
	
	    static createFrom(source: any = {}) {
	        return new WorkspaceGitRepo(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.root = source["root"];
	        this.name = source["name"];
	        this.relPath = source["relPath"];
	        this.branch = source["branch"];
	        this.isRoot = source["isRoot"];
	        this.diffs = this.convertValues(source["diffs"], remote.FileDiff);
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}

}

export namespace jev {
	
	export class CooldownConfig {
	    quota: string;
	    rate_limit: string;
	    outage: string;
	
	    static createFrom(source: any = {}) {
	        return new CooldownConfig(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.quota = source["quota"];
	        this.rate_limit = source["rate_limit"];
	        this.outage = source["outage"];
	    }
	}
	export class RolePin {
	    provider: string;
	    model: string;
	    strict: boolean;
	
	    static createFrom(source: any = {}) {
	        return new RolePin(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.provider = source["provider"];
	        this.model = source["model"];
	        this.strict = source["strict"];
	    }
	}
	export class RepoOverride {
	    deny: string[];
	
	    static createFrom(source: any = {}) {
	        return new RepoOverride(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.deny = source["deny"];
	    }
	}
	export class Weights {
	    quality: number;
	    cost: number;
	    latency: number;
	
	    static createFrom(source: any = {}) {
	        return new Weights(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.quality = source["quality"];
	        this.cost = source["cost"];
	        this.latency = source["latency"];
	    }
	}
	export class Policy {
	    version: number;
	    enabled: boolean;
	    classifyEnabled: boolean;
	    weights: Weights;
	    autoEscalateUsd: number;
	    maxEscalateRatio: number;
	    openRouterMinReserveUsd?: number;
	    dailyUsd: Record<string, number>;
	    cooldown: CooldownConfig;
	    allowDataClass: string[];
	    repos?: Record<string, RepoOverride>;
	    pins?: Record<string, RolePin>;
	
	    static createFrom(source: any = {}) {
	        return new Policy(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.version = source["version"];
	        this.enabled = source["enabled"];
	        this.classifyEnabled = source["classifyEnabled"];
	        this.weights = this.convertValues(source["weights"], Weights);
	        this.autoEscalateUsd = source["autoEscalateUsd"];
	        this.maxEscalateRatio = source["maxEscalateRatio"];
	        this.openRouterMinReserveUsd = source["openRouterMinReserveUsd"];
	        this.dailyUsd = source["dailyUsd"];
	        this.cooldown = this.convertValues(source["cooldown"], CooldownConfig);
	        this.allowDataClass = source["allowDataClass"];
	        this.repos = this.convertValues(source["repos"], RepoOverride, true);
	        this.pins = this.convertValues(source["pins"], RolePin, true);
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	export class ProviderHealthView {
	    provider: string;
	    state: string;
	    reason?: string;
	    until?: string;
	    dailySpentMicroUsd?: number;
	    dailyCapMicroUsd?: number;
	    quotaRemainingPct?: number;
	    quotaStale?: boolean;
	    hasCredential: boolean;
	
	    static createFrom(source: any = {}) {
	        return new ProviderHealthView(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.provider = source["provider"];
	        this.state = source["state"];
	        this.reason = source["reason"];
	        this.until = source["until"];
	        this.dailySpentMicroUsd = source["dailySpentMicroUsd"];
	        this.dailyCapMicroUsd = source["dailyCapMicroUsd"];
	        this.quotaRemainingPct = source["quotaRemainingPct"];
	        this.quotaStale = source["quotaStale"];
	        this.hasCredential = source["hasCredential"];
	    }
	}
	
	

}

export namespace jirax {
	
	export class JiraProfileSummary {
	    id: string;
	    name: string;
	    baseUrl: string;
	    email: string;
	    tokenMasked: string;
	    defaultProject: string;
	    orgPatterns?: string[];
	    isDefault?: boolean;
	    hasToken: boolean;
	
	    static createFrom(source: any = {}) {
	        return new JiraProfileSummary(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.name = source["name"];
	        this.baseUrl = source["baseUrl"];
	        this.email = source["email"];
	        this.tokenMasked = source["tokenMasked"];
	        this.defaultProject = source["defaultProject"];
	        this.orgPatterns = source["orgPatterns"];
	        this.isDefault = source["isDefault"];
	        this.hasToken = source["hasToken"];
	    }
	}
	export class ConnectorStatus {
	    configured: boolean;
	    connected: boolean;
	    source: string;
	    baseUrl: string;
	    email: string;
	    tokenMasked: string;
	    defaultProject: string;
	    user: string;
	    detail: string;
	    activeProfileId?: string;
	    activeProfileName?: string;
	    matchMode?: string;
	    matchedRule?: string;
	    bindingMode?: string;
	    detectedOrg?: string;
	    suggestedPatterns?: string[];
	    profiles?: JiraProfileSummary[];
	
	    static createFrom(source: any = {}) {
	        return new ConnectorStatus(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.configured = source["configured"];
	        this.connected = source["connected"];
	        this.source = source["source"];
	        this.baseUrl = source["baseUrl"];
	        this.email = source["email"];
	        this.tokenMasked = source["tokenMasked"];
	        this.defaultProject = source["defaultProject"];
	        this.user = source["user"];
	        this.detail = source["detail"];
	        this.activeProfileId = source["activeProfileId"];
	        this.activeProfileName = source["activeProfileName"];
	        this.matchMode = source["matchMode"];
	        this.matchedRule = source["matchedRule"];
	        this.bindingMode = source["bindingMode"];
	        this.detectedOrg = source["detectedOrg"];
	        this.suggestedPatterns = source["suggestedPatterns"];
	        this.profiles = this.convertValues(source["profiles"], JiraProfileSummary);
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	export class JiraProfile {
	    id: string;
	    name: string;
	    base_url?: string;
	    email?: string;
	    token?: string;
	    default_project?: string;
	    org_patterns?: string[];
	    is_default?: boolean;
	
	    static createFrom(source: any = {}) {
	        return new JiraProfile(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.name = source["name"];
	        this.base_url = source["base_url"];
	        this.email = source["email"];
	        this.token = source["token"];
	        this.default_project = source["default_project"];
	        this.org_patterns = source["org_patterns"];
	        this.is_default = source["is_default"];
	    }
	}
	
	export class Project {
	    key: string;
	    name: string;
	    projectType?: string;
	
	    static createFrom(source: any = {}) {
	        return new Project(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.key = source["key"];
	        this.name = source["name"];
	        this.projectType = source["projectType"];
	    }
	}

}

export namespace main {
	
	export class AgyBootstrap {
	    status: modelx.AgyStatus;
	    models: modelx.AgyModel[];
	
	    static createFrom(source: any = {}) {
	        return new AgyBootstrap(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.status = this.convertValues(source["status"], modelx.AgyStatus);
	        this.models = this.convertValues(source["models"], modelx.AgyModel);
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	export class ProjectInfo {
	    root: string;
	    name: string;
	    branch: string;
	    stack: idecore.ProjectStack;
	    hasTeam: boolean;
	    teamConfig: team.File;
	    files: idecore.FileNode[];
	
	    static createFrom(source: any = {}) {
	        return new ProjectInfo(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.root = source["root"];
	        this.name = source["name"];
	        this.branch = source["branch"];
	        this.stack = this.convertValues(source["stack"], idecore.ProjectStack);
	        this.hasTeam = source["hasTeam"];
	        this.teamConfig = this.convertValues(source["teamConfig"], team.File);
	        this.files = this.convertValues(source["files"], idecore.FileNode);
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	export class InitialState {
	    initialWorkspace: string;
	    initialProject: ProjectInfo;
	    recentProjects: string[];
	    agyStatus: modelx.AgyStatus;
	    models: modelx.AgyModel[];
	    openCodeStatus: modelx.OpenCodeStatus;
	    openCodeGoModels: modelx.AgyModel[];
	    openCodeModels: modelx.AgyModel[];
	    goVersion: string;
	    os: string;
	
	    static createFrom(source: any = {}) {
	        return new InitialState(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.initialWorkspace = source["initialWorkspace"];
	        this.initialProject = this.convertValues(source["initialProject"], ProjectInfo);
	        this.recentProjects = source["recentProjects"];
	        this.agyStatus = this.convertValues(source["agyStatus"], modelx.AgyStatus);
	        this.models = this.convertValues(source["models"], modelx.AgyModel);
	        this.openCodeStatus = this.convertValues(source["openCodeStatus"], modelx.OpenCodeStatus);
	        this.openCodeGoModels = this.convertValues(source["openCodeGoModels"], modelx.AgyModel);
	        this.openCodeModels = this.convertValues(source["openCodeModels"], modelx.AgyModel);
	        this.goVersion = source["goVersion"];
	        this.os = source["os"];
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	export class OpenCodeBootstrap {
	    status: modelx.OpenCodeStatus;
	    goModels: modelx.AgyModel[];
	    models: modelx.AgyModel[];
	
	    static createFrom(source: any = {}) {
	        return new OpenCodeBootstrap(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.status = this.convertValues(source["status"], modelx.OpenCodeStatus);
	        this.goModels = this.convertValues(source["goModels"], modelx.AgyModel);
	        this.models = this.convertValues(source["models"], modelx.AgyModel);
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	
	export class UpdateCheckResult {
	    available: boolean;
	    currentVersion: string;
	    latestVersion: string;
	    commit?: string;
	    buildDate?: string;
	    artifactName?: string;
	    downloadUrl?: string;
	    sha256?: string;
	    size?: number;
	
	    static createFrom(source: any = {}) {
	        return new UpdateCheckResult(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.available = source["available"];
	        this.currentVersion = source["currentVersion"];
	        this.latestVersion = source["latestVersion"];
	        this.commit = source["commit"];
	        this.buildDate = source["buildDate"];
	        this.artifactName = source["artifactName"];
	        this.downloadUrl = source["downloadUrl"];
	        this.sha256 = source["sha256"];
	        this.size = source["size"];
	    }
	}

}

export namespace mcpx {
	
	export class ServerView {
	    name: string;
	    description: string;
	    transport: string;
	    enabled: boolean;
	    builtin: boolean;
	    status: string;
	    detail?: string;
	    tools?: string[];
	
	    static createFrom(source: any = {}) {
	        return new ServerView(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.name = source["name"];
	        this.description = source["description"];
	        this.transport = source["transport"];
	        this.enabled = source["enabled"];
	        this.builtin = source["builtin"];
	        this.status = source["status"];
	        this.detail = source["detail"];
	        this.tools = source["tools"];
	    }
	}

}

export namespace modelx {
	
	export class ProviderLimits {
	    fiveHourTokens?: number;
	    weeklyTokens?: number;
	
	    static createFrom(source: any = {}) {
	        return new ProviderLimits(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.fiveHourTokens = source["fiveHourTokens"];
	        this.weeklyTokens = source["weeklyTokens"];
	    }
	}
	export class UsageWindow {
	    inputTokens: number;
	    outputTokens: number;
	    totalTokens: number;
	    calls: number;
	    oldestAt?: string;
	    costMicroUsd?: number;
	
	    static createFrom(source: any = {}) {
	        return new UsageWindow(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.inputTokens = source["inputTokens"];
	        this.outputTokens = source["outputTokens"];
	        this.totalTokens = source["totalTokens"];
	        this.calls = source["calls"];
	        this.oldestAt = source["oldestAt"];
	        this.costMicroUsd = source["costMicroUsd"];
	    }
	}
	export class AIProviderUsage {
	    provider: string;
	    label: string;
	    window5h: UsageWindow;
	    windowWeekly: UsageWindow;
	    windowMonthly: UsageWindow;
	    limits: ProviderLimits;
	
	    static createFrom(source: any = {}) {
	        return new AIProviderUsage(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.provider = source["provider"];
	        this.label = source["label"];
	        this.window5h = this.convertValues(source["window5h"], UsageWindow);
	        this.windowWeekly = this.convertValues(source["windowWeekly"], UsageWindow);
	        this.windowMonthly = this.convertValues(source["windowMonthly"], UsageWindow);
	        this.limits = this.convertValues(source["limits"], ProviderLimits);
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	export class AgentUsageCost {
	    agent: string;
	    role?: string;
	    provider?: string;
	    model?: string;
	    calls: number;
	    totalTokens: number;
	    costMicroUsd: number;
	
	    static createFrom(source: any = {}) {
	        return new AgentUsageCost(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.agent = source["agent"];
	        this.role = source["role"];
	        this.provider = source["provider"];
	        this.model = source["model"];
	        this.calls = source["calls"];
	        this.totalTokens = source["totalTokens"];
	        this.costMicroUsd = source["costMicroUsd"];
	    }
	}
	export class RoleUsageCost {
	    role: string;
	    calls: number;
	    totalTokens: number;
	    costMicroUsd: number;
	
	    static createFrom(source: any = {}) {
	        return new RoleUsageCost(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.role = source["role"];
	        this.calls = source["calls"];
	        this.totalTokens = source["totalTokens"];
	        this.costMicroUsd = source["costMicroUsd"];
	    }
	}
	export class RemoteQuotaWindow {
	    percent: number;
	    resetsAt?: string;
	    status?: string;
	    hasData: boolean;
	
	    static createFrom(source: any = {}) {
	        return new RemoteQuotaWindow(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.percent = source["percent"];
	        this.resetsAt = source["resetsAt"];
	        this.status = source["status"];
	        this.hasData = source["hasData"];
	    }
	}
	export class OpenCodeQuota {
	    available: boolean;
	    detail?: string;
	    rolling5h: RemoteQuotaWindow;
	    weekly: RemoteQuotaWindow;
	    monthly: RemoteQuotaWindow;
	
	    static createFrom(source: any = {}) {
	        return new OpenCodeQuota(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.available = source["available"];
	        this.detail = source["detail"];
	        this.rolling5h = this.convertValues(source["rolling5h"], RemoteQuotaWindow);
	        this.weekly = this.convertValues(source["weekly"], RemoteQuotaWindow);
	        this.monthly = this.convertValues(source["monthly"], RemoteQuotaWindow);
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	export class AgyAccountQuota {
	    profileId: string;
	    name: string;
	    email?: string;
	    quota: AgyQuota;
	
	    static createFrom(source: any = {}) {
	        return new AgyAccountQuota(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.profileId = source["profileId"];
	        this.name = source["name"];
	        this.email = source["email"];
	        this.quota = this.convertValues(source["quota"], AgyQuota);
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	export class AgyQuotaBucket {
	    id: string;
	    name: string;
	    window: string;
	    usedPercent: number;
	    remainingPercent: number;
	    resetTime?: string;
	    description?: string;
	
	    static createFrom(source: any = {}) {
	        return new AgyQuotaBucket(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.name = source["name"];
	        this.window = source["window"];
	        this.usedPercent = source["usedPercent"];
	        this.remainingPercent = source["remainingPercent"];
	        this.resetTime = source["resetTime"];
	        this.description = source["description"];
	    }
	}
	export class AgyQuotaGroup {
	    name: string;
	    description?: string;
	    buckets: AgyQuotaBucket[];
	
	    static createFrom(source: any = {}) {
	        return new AgyQuotaGroup(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.name = source["name"];
	        this.description = source["description"];
	        this.buckets = this.convertValues(source["buckets"], AgyQuotaBucket);
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	export class AgyQuota {
	    available: boolean;
	    detail?: string;
	    groups?: AgyQuotaGroup[];
	
	    static createFrom(source: any = {}) {
	        return new AgyQuota(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.available = source["available"];
	        this.detail = source["detail"];
	        this.groups = this.convertValues(source["groups"], AgyQuotaGroup);
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	export class AIUsageReport {
	    generatedAt: string;
	    agyQuota: AgyQuota;
	    agyAccounts?: AgyAccountQuota[];
	    openCodeQuota: OpenCodeQuota;
	    providers: AIProviderUsage[];
	    costSummary5hMicroUsd?: number;
	    costSummaryWeeklyMicroUsd?: number;
	    costSummaryMonthlyMicroUsd?: number;
	    costByRole?: RoleUsageCost[];
	    costByAgent?: AgentUsageCost[];
	
	    static createFrom(source: any = {}) {
	        return new AIUsageReport(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.generatedAt = source["generatedAt"];
	        this.agyQuota = this.convertValues(source["agyQuota"], AgyQuota);
	        this.agyAccounts = this.convertValues(source["agyAccounts"], AgyAccountQuota);
	        this.openCodeQuota = this.convertValues(source["openCodeQuota"], OpenCodeQuota);
	        this.providers = this.convertValues(source["providers"], AIProviderUsage);
	        this.costSummary5hMicroUsd = source["costSummary5hMicroUsd"];
	        this.costSummaryWeeklyMicroUsd = source["costSummaryWeeklyMicroUsd"];
	        this.costSummaryMonthlyMicroUsd = source["costSummaryMonthlyMicroUsd"];
	        this.costByRole = this.convertValues(source["costByRole"], RoleUsageCost);
	        this.costByAgent = this.convertValues(source["costByAgent"], AgentUsageCost);
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	
	
	export class AgyModel {
	    slug: string;
	    name: string;
	
	    static createFrom(source: any = {}) {
	        return new AgyModel(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.slug = source["slug"];
	        this.name = source["name"];
	    }
	}
	
	
	
	export class AgyStatus {
	    Found: boolean;
	    Path: string;
	    LoggedIn: boolean;
	    Detail: string;
	
	    static createFrom(source: any = {}) {
	        return new AgyStatus(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.Found = source["Found"];
	        this.Path = source["Path"];
	        this.LoggedIn = source["LoggedIn"];
	        this.Detail = source["Detail"];
	    }
	}
	export class CursorStatus {
	    found: boolean;
	    path: string;
	    hasKey: boolean;
	    detail: string;
	    sandbox?: string;
	    sandboxWarning?: string;
	
	    static createFrom(source: any = {}) {
	        return new CursorStatus(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.found = source["found"];
	        this.path = source["path"];
	        this.hasKey = source["hasKey"];
	        this.detail = source["detail"];
	        this.sandbox = source["sandbox"];
	        this.sandboxWarning = source["sandboxWarning"];
	    }
	}
	
	export class OpenCodeStatus {
	    configured: boolean;
	    source: string;
	    baseUrl: string;
	    keyMasked: string;
	    goCount: number;
	    zenCount: number;
	    modelCount: number;
	    detail: string;
	
	    static createFrom(source: any = {}) {
	        return new OpenCodeStatus(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.configured = source["configured"];
	        this.source = source["source"];
	        this.baseUrl = source["baseUrl"];
	        this.keyMasked = source["keyMasked"];
	        this.goCount = source["goCount"];
	        this.zenCount = source["zenCount"];
	        this.modelCount = source["modelCount"];
	        this.detail = source["detail"];
	    }
	}
	export class OpenRouterModel {
	    id: string;
	    name: string;
	    contextLength?: number;
	    promptPrice?: number;
	    completionPrice?: number;
	    discountPct?: number;
	    supportsTools?: boolean;
	    supportsVision?: boolean;
	
	    static createFrom(source: any = {}) {
	        return new OpenRouterModel(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.name = source["name"];
	        this.contextLength = source["contextLength"];
	        this.promptPrice = source["promptPrice"];
	        this.completionPrice = source["completionPrice"];
	        this.discountPct = source["discountPct"];
	        this.supportsTools = source["supportsTools"];
	        this.supportsVision = source["supportsVision"];
	    }
	}
	export class ResolvedProviderInfo {
	    provider: string;
	    profileId?: string;
	    profileName?: string;
	    matchMode: string;
	    matchedRule?: string;
	    bindingMode?: string;
	
	    static createFrom(source: any = {}) {
	        return new ResolvedProviderInfo(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.provider = source["provider"];
	        this.profileId = source["profileId"];
	        this.profileName = source["profileName"];
	        this.matchMode = source["matchMode"];
	        this.matchedRule = source["matchedRule"];
	        this.bindingMode = source["bindingMode"];
	    }
	}
	export class ProviderProfileSummary {
	    id: string;
	    provider: string;
	    name: string;
	    authMode?: string;
	    command?: string;
	    envMasked?: Record<string, string>;
	    configValues?: Record<string, string>;
	    orgPatterns?: string[];
	    isDefault?: boolean;
	    ready: boolean;
	    readyDetail?: string;
	
	    static createFrom(source: any = {}) {
	        return new ProviderProfileSummary(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.provider = source["provider"];
	        this.name = source["name"];
	        this.authMode = source["authMode"];
	        this.command = source["command"];
	        this.envMasked = source["envMasked"];
	        this.configValues = source["configValues"];
	        this.orgPatterns = source["orgPatterns"];
	        this.isDefault = source["isDefault"];
	        this.ready = source["ready"];
	        this.readyDetail = source["readyDetail"];
	    }
	}
	export class ProfilesOverview {
	    identity: orgx.Identity;
	    profiles: ProviderProfileSummary[];
	    resolved: Record<string, ResolvedProviderInfo>;
	
	    static createFrom(source: any = {}) {
	        return new ProfilesOverview(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.identity = this.convertValues(source["identity"], orgx.Identity);
	        this.profiles = this.convertValues(source["profiles"], ProviderProfileSummary);
	        this.resolved = this.convertValues(source["resolved"], ResolvedProviderInfo, true);
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	
	
	export class ProviderScanAccount {
	    id?: string;
	    name: string;
	    source: string;
	    active: boolean;
	    ready: boolean;
	    maskedHint?: string;
	
	    static createFrom(source: any = {}) {
	        return new ProviderScanAccount(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.name = source["name"];
	        this.source = source["source"];
	        this.active = source["active"];
	        this.ready = source["ready"];
	        this.maskedHint = source["maskedHint"];
	    }
	}
	export class ProviderScanItem {
	    id: string;
	    name: string;
	    found: boolean;
	    connected: boolean;
	    statusLabel: string;
	    source?: string;
	    path?: string;
	    detail: string;
	    modelCount: number;
	    modelsSample?: string[];
	    accounts: ProviderScanAccount[];
	
	    static createFrom(source: any = {}) {
	        return new ProviderScanItem(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.name = source["name"];
	        this.found = source["found"];
	        this.connected = source["connected"];
	        this.statusLabel = source["statusLabel"];
	        this.source = source["source"];
	        this.path = source["path"];
	        this.detail = source["detail"];
	        this.modelCount = source["modelCount"];
	        this.modelsSample = source["modelsSample"];
	        this.accounts = this.convertValues(source["accounts"], ProviderScanAccount);
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	export class ProvidersScanReport {
	    scannedAt: string;
	    totalProviders: number;
	    connectedProviders: number;
	    totalAccounts: number;
	    providers: ProviderScanItem[];
	
	    static createFrom(source: any = {}) {
	        return new ProvidersScanReport(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.scannedAt = source["scannedAt"];
	        this.totalProviders = source["totalProviders"];
	        this.connectedProviders = source["connectedProviders"];
	        this.totalAccounts = source["totalAccounts"];
	        this.providers = this.convertValues(source["providers"], ProviderScanItem);
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	
	
	
	export class SaveProfileRequest {
	    id: string;
	    provider: string;
	    name: string;
	    authMode?: string;
	    command?: string;
	    env?: Record<string, string>;
	    orgPatterns?: string[];
	    isDefault?: boolean;
	    bindWorkspace?: string;
	
	    static createFrom(source: any = {}) {
	        return new SaveProfileRequest(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.provider = source["provider"];
	        this.name = source["name"];
	        this.authMode = source["authMode"];
	        this.command = source["command"];
	        this.env = source["env"];
	        this.orgPatterns = source["orgPatterns"];
	        this.isDefault = source["isDefault"];
	        this.bindWorkspace = source["bindWorkspace"];
	    }
	}

}

export namespace orchestrator {
	
	export class AgentCost {
	    agent: string;
	    role: string;
	    provider?: string;
	    model?: string;
	    calls: number;
	    totalTokens: number;
	    costMicroUsd: number;
	
	    static createFrom(source: any = {}) {
	        return new AgentCost(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.agent = source["agent"];
	        this.role = source["role"];
	        this.provider = source["provider"];
	        this.model = source["model"];
	        this.calls = source["calls"];
	        this.totalTokens = source["totalTokens"];
	        this.costMicroUsd = source["costMicroUsd"];
	    }
	}
	export class NestedDelivery {
	    relPath: string;
	    repoDir: string;
	    worktreeDir: string;
	    branch: string;
	    baseBranch?: string;
	    pullRequest?: githubx.PullRequest;
	
	    static createFrom(source: any = {}) {
	        return new NestedDelivery(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.relPath = source["relPath"];
	        this.repoDir = source["repoDir"];
	        this.worktreeDir = source["worktreeDir"];
	        this.branch = source["branch"];
	        this.baseBranch = source["baseBranch"];
	        this.pullRequest = this.convertValues(source["pullRequest"], githubx.PullRequest);
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	export class RoleCost {
	    role: string;
	    calls: number;
	    totalTokens: number;
	    costMicroUsd: number;
	
	    static createFrom(source: any = {}) {
	        return new RoleCost(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.role = source["role"];
	        this.calls = source["calls"];
	        this.totalTokens = source["totalTokens"];
	        this.costMicroUsd = source["costMicroUsd"];
	    }
	}
	export class Result {
	    SessionID: string;
	    Summary: string;
	    Branches: string[];
	    integrationBranch?: string;
	    deliveryBranch?: string;
	    worktreeDir?: string;
	    PullRequests: githubx.PullRequest[];
	    nestedDeliveries?: NestedDelivery[];
	    inputTokens: number;
	    outputTokens: number;
	    totalTokens: number;
	    costMicroUsd?: number;
	    costByAgent?: AgentCost[];
	    costByRole?: RoleCost[];
	    pendingQa?: boolean;
	
	    static createFrom(source: any = {}) {
	        return new Result(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.SessionID = source["SessionID"];
	        this.Summary = source["Summary"];
	        this.Branches = source["Branches"];
	        this.integrationBranch = source["integrationBranch"];
	        this.deliveryBranch = source["deliveryBranch"];
	        this.worktreeDir = source["worktreeDir"];
	        this.PullRequests = this.convertValues(source["PullRequests"], githubx.PullRequest);
	        this.nestedDeliveries = this.convertValues(source["nestedDeliveries"], NestedDelivery);
	        this.inputTokens = source["inputTokens"];
	        this.outputTokens = source["outputTokens"];
	        this.totalTokens = source["totalTokens"];
	        this.costMicroUsd = source["costMicroUsd"];
	        this.costByAgent = this.convertValues(source["costByAgent"], AgentCost);
	        this.costByRole = this.convertValues(source["costByRole"], RoleCost);
	        this.pendingQa = source["pendingQa"];
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}

}

export namespace orgx {
	
	export class Identity {
	    workspace: string;
	    gitRemoteUrl?: string;
	    gitHost?: string;
	    gitOrg?: string;
	    gitRepo?: string;
	    workspaceLeaf?: string;
	    workspaceParent?: string;
	    suggestedPatterns?: string[];
	
	    static createFrom(source: any = {}) {
	        return new Identity(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.workspace = source["workspace"];
	        this.gitRemoteUrl = source["gitRemoteUrl"];
	        this.gitHost = source["gitHost"];
	        this.gitOrg = source["gitOrg"];
	        this.gitRepo = source["gitRepo"];
	        this.workspaceLeaf = source["workspaceLeaf"];
	        this.workspaceParent = source["workspaceParent"];
	        this.suggestedPatterns = source["suggestedPatterns"];
	    }
	}

}

export namespace relayclient {
	
	export class LinkCode {
	    code: string;
	    expiresAt: string;
	    uri: string;
	    qrDataUrl: string;
	
	    static createFrom(source: any = {}) {
	        return new LinkCode(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.code = source["code"];
	        this.expiresAt = source["expiresAt"];
	        this.uri = source["uri"];
	        this.qrDataUrl = source["qrDataUrl"];
	    }
	}
	export class Status {
	    enabled: boolean;
	    loggedIn: boolean;
	    email: string;
	    role: string;
	    baseUrl: string;
	    deviceId: string;
	    deviceName: string;
	    linked: boolean;
	    online: boolean;
	    lastError: string;
	    e2e: boolean;
	
	    static createFrom(source: any = {}) {
	        return new Status(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.enabled = source["enabled"];
	        this.loggedIn = source["loggedIn"];
	        this.email = source["email"];
	        this.role = source["role"];
	        this.baseUrl = source["baseUrl"];
	        this.deviceId = source["deviceId"];
	        this.deviceName = source["deviceName"];
	        this.linked = source["linked"];
	        this.online = source["online"];
	        this.lastError = source["lastError"];
	        this.e2e = source["e2e"];
	    }
	}

}

export namespace remote {
	
	export class AutoReviewEntry {
	    prNumber: number;
	    title: string;
	    author?: string;
	    headRef?: string;
	    baseRef?: string;
	    status: string;
	    summary?: string;
	    tokens?: number;
	    durationMs?: number;
	    sessionId?: string;
	    reviewedAt: string;
	    error?: string;
	
	    static createFrom(source: any = {}) {
	        return new AutoReviewEntry(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.prNumber = source["prNumber"];
	        this.title = source["title"];
	        this.author = source["author"];
	        this.headRef = source["headRef"];
	        this.baseRef = source["baseRef"];
	        this.status = source["status"];
	        this.summary = source["summary"];
	        this.tokens = source["tokens"];
	        this.durationMs = source["durationMs"];
	        this.sessionId = source["sessionId"];
	        this.reviewedAt = source["reviewedAt"];
	        this.error = source["error"];
	    }
	}
	export class AutoReviewStatus {
	    workspace: string;
	    enabled: boolean;
	    state: string;
	    config: team.AutoReviewSettings;
	    activePr?: number;
	    activePrTitle?: string;
	    queueCount: number;
	    reviewedLastHour: number;
	    lastPollAt?: string;
	    nextPollAt?: string;
	    lastError?: string;
	    history?: AutoReviewEntry[];
	
	    static createFrom(source: any = {}) {
	        return new AutoReviewStatus(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.workspace = source["workspace"];
	        this.enabled = source["enabled"];
	        this.state = source["state"];
	        this.config = this.convertValues(source["config"], team.AutoReviewSettings);
	        this.activePr = source["activePr"];
	        this.activePrTitle = source["activePrTitle"];
	        this.queueCount = source["queueCount"];
	        this.reviewedLastHour = source["reviewedLastHour"];
	        this.lastPollAt = source["lastPollAt"];
	        this.nextPollAt = source["nextPollAt"];
	        this.lastError = source["lastError"];
	        this.history = this.convertValues(source["history"], AutoReviewEntry);
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	export class DiffLine {
	    kind: string;
	    text: string;
	
	    static createFrom(source: any = {}) {
	        return new DiffLine(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.kind = source["kind"];
	        this.text = source["text"];
	    }
	}
	export class FileDiff {
	    path: string;
	    status?: string;
	    staged?: boolean;
	    added: number;
	    removed: number;
	    lines: DiffLine[];
	
	    static createFrom(source: any = {}) {
	        return new FileDiff(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.path = source["path"];
	        this.status = source["status"];
	        this.staged = source["staged"];
	        this.added = source["added"];
	        this.removed = source["removed"];
	        this.lines = this.convertValues(source["lines"], DiffLine);
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	export class ImageInput {
	    name: string;
	    mimeType: string;
	    dataBase64: string;
	
	    static createFrom(source: any = {}) {
	        return new ImageInput(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.name = source["name"];
	        this.mimeType = source["mimeType"];
	        this.dataBase64 = source["dataBase64"];
	    }
	}
	export class PairingInfo {
	    uri: string;
	    urls: string[];
	    host: string;
	    qrDataUrl: string;
	    exp: number;
	
	    static createFrom(source: any = {}) {
	        return new PairingInfo(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.uri = source["uri"];
	        this.urls = source["urls"];
	        this.host = source["host"];
	        this.qrDataUrl = source["qrDataUrl"];
	        this.exp = source["exp"];
	    }
	}
	export class ProjectSubtask {
	    key: string;
	    summary: string;
	    status?: string;
	    done: boolean;
	    url?: string;
	
	    static createFrom(source: any = {}) {
	        return new ProjectSubtask(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.key = source["key"];
	        this.summary = source["summary"];
	        this.status = source["status"];
	        this.done = source["done"];
	        this.url = source["url"];
	    }
	}
	export class ProjectIssue {
	    source: string;
	    key: string;
	    title: string;
	    body?: string;
	    status?: string;
	    issueType?: string;
	    isSubtask?: boolean;
	    parentKey?: string;
	    parentTitle?: string;
	    parentUrl?: string;
	    subtaskTotal?: number;
	    subtaskDone?: number;
	    subtaskPending?: number;
	    subtasks?: ProjectSubtask[];
	    author?: string;
	    url?: string;
	    isMine?: boolean;
	
	    static createFrom(source: any = {}) {
	        return new ProjectIssue(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.source = source["source"];
	        this.key = source["key"];
	        this.title = source["title"];
	        this.body = source["body"];
	        this.status = source["status"];
	        this.issueType = source["issueType"];
	        this.isSubtask = source["isSubtask"];
	        this.parentKey = source["parentKey"];
	        this.parentTitle = source["parentTitle"];
	        this.parentUrl = source["parentUrl"];
	        this.subtaskTotal = source["subtaskTotal"];
	        this.subtaskDone = source["subtaskDone"];
	        this.subtaskPending = source["subtaskPending"];
	        this.subtasks = this.convertValues(source["subtasks"], ProjectSubtask);
	        this.author = source["author"];
	        this.url = source["url"];
	        this.isMine = source["isMine"];
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	export class ProjectPullRequest {
	    number: number;
	    title: string;
	    body?: string;
	    url: string;
	    state: string;
	    isDraft: boolean;
	    author?: string;
	    headRef?: string;
	    baseRef?: string;
	    updatedAt?: string;
	    reviewDecision?: string;
	    requestedReviewers?: string[];
	    isMine?: boolean;
	    reviewRequested?: boolean;
	    myResponse?: string;
	    needsAttention?: boolean;
	    attentionReason?: string;
	    mentionedMe?: boolean;
	    lastActivityAt?: string;
	    lastActivityAuthor?: string;
	    lastActivityKind?: string;
	    lastActivitySnippet?: string;
	
	    static createFrom(source: any = {}) {
	        return new ProjectPullRequest(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.number = source["number"];
	        this.title = source["title"];
	        this.body = source["body"];
	        this.url = source["url"];
	        this.state = source["state"];
	        this.isDraft = source["isDraft"];
	        this.author = source["author"];
	        this.headRef = source["headRef"];
	        this.baseRef = source["baseRef"];
	        this.updatedAt = source["updatedAt"];
	        this.reviewDecision = source["reviewDecision"];
	        this.requestedReviewers = source["requestedReviewers"];
	        this.isMine = source["isMine"];
	        this.reviewRequested = source["reviewRequested"];
	        this.myResponse = source["myResponse"];
	        this.needsAttention = source["needsAttention"];
	        this.attentionReason = source["attentionReason"];
	        this.mentionedMe = source["mentionedMe"];
	        this.lastActivityAt = source["lastActivityAt"];
	        this.lastActivityAuthor = source["lastActivityAuthor"];
	        this.lastActivityKind = source["lastActivityKind"];
	        this.lastActivitySnippet = source["lastActivitySnippet"];
	    }
	}
	
	export class StatusInfo {
	    enabled: boolean;
	    running: boolean;
	    port: number;
	    urls: string[];
	    host: string;
	    version: string;
	
	    static createFrom(source: any = {}) {
	        return new StatusInfo(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.enabled = source["enabled"];
	        this.running = source["running"];
	        this.port = source["port"];
	        this.urls = source["urls"];
	        this.host = source["host"];
	        this.version = source["version"];
	    }
	}

}

export namespace resident {
	
	export class AuditEntry {
	    ts: string;
	    phase: string;
	    accountId: string;
	    deviceId?: string;
	    origin: string;
	    workstream?: string;
	    tool: string;
	    tier: string;
	    decision: string;
	    approvalId?: string;
	    sandbox?: string;
	    target?: string;
	    targetClass?: string;
	    targetHash?: string;
	    outcome?: string;
	    errorClass?: string;
	    reason?: string;
	
	    static createFrom(source: any = {}) {
	        return new AuditEntry(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.ts = source["ts"];
	        this.phase = source["phase"];
	        this.accountId = source["accountId"];
	        this.deviceId = source["deviceId"];
	        this.origin = source["origin"];
	        this.workstream = source["workstream"];
	        this.tool = source["tool"];
	        this.tier = source["tier"];
	        this.decision = source["decision"];
	        this.approvalId = source["approvalId"];
	        this.sandbox = source["sandbox"];
	        this.target = source["target"];
	        this.targetClass = source["targetClass"];
	        this.targetHash = source["targetHash"];
	        this.outcome = source["outcome"];
	        this.errorClass = source["errorClass"];
	        this.reason = source["reason"];
	    }
	}
	export class LocalTurnMessage {
	    role: string;
	    text: string;
	    // Go type: time
	    at: any;
	
	    static createFrom(source: any = {}) {
	        return new LocalTurnMessage(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.role = source["role"];
	        this.text = source["text"];
	        this.at = this.convertValues(source["at"], null);
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	export class MemoryFact {
	    id: string;
	    workstream?: string;
	    text: string;
	    pinned?: boolean;
	    source?: string;
	    // Go type: time
	    createdAt: any;
	
	    static createFrom(source: any = {}) {
	        return new MemoryFact(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.workstream = source["workstream"];
	        this.text = source["text"];
	        this.pinned = source["pinned"];
	        this.source = source["source"];
	        this.createdAt = this.convertValues(source["createdAt"], null);
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	export class Routine {
	    id: string;
	    expr: string;
	    timezone?: string;
	    text: string;
	    workstream?: string;
	    classes?: string[];
	    enabled: boolean;
	    // Go type: time
	    lastFired?: any;
	    day?: string;
	    dayTurns?: number;
	
	    static createFrom(source: any = {}) {
	        return new Routine(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.expr = source["expr"];
	        this.timezone = source["timezone"];
	        this.text = source["text"];
	        this.workstream = source["workstream"];
	        this.classes = source["classes"];
	        this.enabled = source["enabled"];
	        this.lastFired = this.convertValues(source["lastFired"], null);
	        this.day = source["day"];
	        this.dayTurns = source["dayTurns"];
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	export class Status {
	    state: string;
	    readOnly: boolean;
	    accountId: string;
	    workstream: string;
	    provider: string;
	    model: string;
	    memoryDir?: string;
	    auditPath?: string;
	    lastError?: string;
	    sandboxAvailable: boolean;
	    sandboxMechanism?: string;
	    sandboxReason?: string;
	    vaultAvailable: boolean;
	    vaultReason?: string;
	    allowedBinaries: string[];
	    allowedHosts: string[];
	    vaultSecrets: string[];
	    eventTriggers: boolean;
	    experimentalUnisolated: boolean;
	    isolatedSandbox: boolean;
	    experimentalWarning?: string;
	    isolatedBrowser: boolean;
	    isolatedBrowserReason?: string;
	    isolatedDesktop: boolean;
	    isolatedDesktopReason?: string;
	
	    static createFrom(source: any = {}) {
	        return new Status(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.state = source["state"];
	        this.readOnly = source["readOnly"];
	        this.accountId = source["accountId"];
	        this.workstream = source["workstream"];
	        this.provider = source["provider"];
	        this.model = source["model"];
	        this.memoryDir = source["memoryDir"];
	        this.auditPath = source["auditPath"];
	        this.lastError = source["lastError"];
	        this.sandboxAvailable = source["sandboxAvailable"];
	        this.sandboxMechanism = source["sandboxMechanism"];
	        this.sandboxReason = source["sandboxReason"];
	        this.vaultAvailable = source["vaultAvailable"];
	        this.vaultReason = source["vaultReason"];
	        this.allowedBinaries = source["allowedBinaries"];
	        this.allowedHosts = source["allowedHosts"];
	        this.vaultSecrets = source["vaultSecrets"];
	        this.eventTriggers = source["eventTriggers"];
	        this.experimentalUnisolated = source["experimentalUnisolated"];
	        this.isolatedSandbox = source["isolatedSandbox"];
	        this.experimentalWarning = source["experimentalWarning"];
	        this.isolatedBrowser = source["isolatedBrowser"];
	        this.isolatedBrowserReason = source["isolatedBrowserReason"];
	        this.isolatedDesktop = source["isolatedDesktop"];
	        this.isolatedDesktopReason = source["isolatedDesktopReason"];
	    }
	}
	export class TurnResult {
	    sessionId: string;
	    workstream: string;
	    finalText: string;
	    provider: string;
	    model: string;
	    inputTokens: number;
	    outputTokens: number;
	    totalTokens: number;
	    toolCalls: number;
	    promptBuildMs: number;
	
	    static createFrom(source: any = {}) {
	        return new TurnResult(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.sessionId = source["sessionId"];
	        this.workstream = source["workstream"];
	        this.finalText = source["finalText"];
	        this.provider = source["provider"];
	        this.model = source["model"];
	        this.inputTokens = source["inputTokens"];
	        this.outputTokens = source["outputTokens"];
	        this.totalTokens = source["totalTokens"];
	        this.toolCalls = source["toolCalls"];
	        this.promptBuildMs = source["promptBuildMs"];
	    }
	}

}

export namespace sessionx {
	
	export class RestoredMessage {
	    id: string;
	    kind: string;
	    label: string;
	    text: string;
	    tokens?: number;
	
	    static createFrom(source: any = {}) {
	        return new RestoredMessage(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.kind = source["kind"];
	        this.label = source["label"];
	        this.text = source["text"];
	        this.tokens = source["tokens"];
	    }
	}
	export class TokenStat {
	    inputTokens: number;
	    outputTokens: number;
	    totalTokens: number;
	    calls: number;
	
	    static createFrom(source: any = {}) {
	        return new TokenStat(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.inputTokens = source["inputTokens"];
	        this.outputTokens = source["outputTokens"];
	        this.totalTokens = source["totalTokens"];
	        this.calls = source["calls"];
	    }
	}
	export class SessionDetail {
	    id: string;
	    title: string;
	    jiraKey?: string;
	    inputTokens: number;
	    outputTokens: number;
	    totalTokens: number;
	    agentUsage: Record<string, TokenStat>;
	    messages: RestoredMessage[];
	
	    static createFrom(source: any = {}) {
	        return new SessionDetail(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.title = source["title"];
	        this.jiraKey = source["jiraKey"];
	        this.inputTokens = source["inputTokens"];
	        this.outputTokens = source["outputTokens"];
	        this.totalTokens = source["totalTokens"];
	        this.agentUsage = this.convertValues(source["agentUsage"], TokenStat, true);
	        this.messages = this.convertValues(source["messages"], RestoredMessage);
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	export class SessionSummary {
	    id: string;
	    updatedAt: string;
	    title: string;
	    jiraKey?: string;
	    inputTokens: number;
	    outputTokens: number;
	    totalTokens: number;
	
	    static createFrom(source: any = {}) {
	        return new SessionSummary(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.updatedAt = source["updatedAt"];
	        this.title = source["title"];
	        this.jiraKey = source["jiraKey"];
	        this.inputTokens = source["inputTokens"];
	        this.outputTokens = source["outputTokens"];
	        this.totalTokens = source["totalTokens"];
	    }
	}
	
	export class WorkspaceSessionsSummary {
	    sessions: SessionSummary[];
	    projectInputTokens: number;
	    projectOutputTokens: number;
	    projectTotalTokens: number;
	
	    static createFrom(source: any = {}) {
	        return new WorkspaceSessionsSummary(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.sessions = this.convertValues(source["sessions"], SessionSummary);
	        this.projectInputTokens = source["projectInputTokens"];
	        this.projectOutputTokens = source["projectOutputTokens"];
	        this.projectTotalTokens = source["projectTotalTokens"];
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}

}

export namespace team {
	
	export class Agent {
	    name: string;
	    role: string;
	    provider: string;
	    model: string;
	    worker?: string;
	    instructions?: string;
	
	    static createFrom(source: any = {}) {
	        return new Agent(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.name = source["name"];
	        this.role = source["role"];
	        this.provider = source["provider"];
	        this.model = source["model"];
	        this.worker = source["worker"];
	        this.instructions = source["instructions"];
	    }
	}
	export class AutoReviewSettings {
	    enabled: boolean;
	    poll_interval_seconds?: number;
	    run_tests?: boolean;
	    review_action?: string;
	    include_drafts?: boolean;
	    max_prs_per_hour?: number;
	    max_tokens_per_pr?: number;
	    model?: string;
	
	    static createFrom(source: any = {}) {
	        return new AutoReviewSettings(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.enabled = source["enabled"];
	        this.poll_interval_seconds = source["poll_interval_seconds"];
	        this.run_tests = source["run_tests"];
	        this.review_action = source["review_action"];
	        this.include_drafts = source["include_drafts"];
	        this.max_prs_per_hour = source["max_prs_per_hour"];
	        this.max_tokens_per_pr = source["max_tokens_per_pr"];
	        this.model = source["model"];
	    }
	}
	export class Team {
	    name: string;
	    description?: string;
	    agents: Agent[];
	
	    static createFrom(source: any = {}) {
	        return new Team(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.name = source["name"];
	        this.description = source["description"];
	        this.agents = this.convertValues(source["agents"], Agent);
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	export class SkillAssignment {
	    name: string;
	    source?: string;
	    roles?: string[];
	
	    static createFrom(source: any = {}) {
	        return new SkillAssignment(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.name = source["name"];
	        this.source = source["source"];
	        this.roles = source["roles"];
	    }
	}
	export class Settings {
	    permission_mode?: string;
	    jira_enabled: boolean;
	    jira_project?: string;
	    worktrees?: boolean;
	    shared_directories?: string[];
	    auto_commit: boolean;
	    auto_pr: boolean;
	    homolog_branch?: string;
	    protected_branches?: string[];
	    require_main_merge_approval?: boolean;
	    max_qa_loops: number;
	    max_dynamic_devs?: number;
	    max_llm_calls: number;
	    max_dev_calls?: number;
	    max_dev_explore_calls?: number;
	    max_dev_input_tokens?: number;
	    max_tokens?: number;
	    in_turn_keep_tool_results?: number;
	    review_model?: string;
	    inline_model?: string;
	    auto_review?: AutoReviewSettings;
	    base_branch: string;
	    shell_allowlist?: string[];
	    confirm_shell?: boolean;
	    shell_unsandboxed?: boolean;
	    skill_assignments?: SkillAssignment[];
	
	    static createFrom(source: any = {}) {
	        return new Settings(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.permission_mode = source["permission_mode"];
	        this.jira_enabled = source["jira_enabled"];
	        this.jira_project = source["jira_project"];
	        this.worktrees = source["worktrees"];
	        this.shared_directories = source["shared_directories"];
	        this.auto_commit = source["auto_commit"];
	        this.auto_pr = source["auto_pr"];
	        this.homolog_branch = source["homolog_branch"];
	        this.protected_branches = source["protected_branches"];
	        this.require_main_merge_approval = source["require_main_merge_approval"];
	        this.max_qa_loops = source["max_qa_loops"];
	        this.max_dynamic_devs = source["max_dynamic_devs"];
	        this.max_llm_calls = source["max_llm_calls"];
	        this.max_dev_calls = source["max_dev_calls"];
	        this.max_dev_explore_calls = source["max_dev_explore_calls"];
	        this.max_dev_input_tokens = source["max_dev_input_tokens"];
	        this.max_tokens = source["max_tokens"];
	        this.in_turn_keep_tool_results = source["in_turn_keep_tool_results"];
	        this.review_model = source["review_model"];
	        this.inline_model = source["inline_model"];
	        this.auto_review = this.convertValues(source["auto_review"], AutoReviewSettings);
	        this.base_branch = source["base_branch"];
	        this.shell_allowlist = source["shell_allowlist"];
	        this.confirm_shell = source["confirm_shell"];
	        this.shell_unsandboxed = source["shell_unsandboxed"];
	        this.skill_assignments = this.convertValues(source["skill_assignments"], SkillAssignment);
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	export class File {
	    version: number;
	    active: string;
	    settings: Settings;
	    teams: Team[];
	
	    static createFrom(source: any = {}) {
	        return new File(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.version = source["version"];
	        this.active = source["active"];
	        this.settings = this.convertValues(source["settings"], Settings);
	        this.teams = this.convertValues(source["teams"], Team);
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	
	

}

