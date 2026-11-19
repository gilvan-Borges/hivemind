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

export namespace idecore {
	
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
	export class CommitDetail {
	    hash: string;
	    short: string;
	    author: string;
	    date: string;
	    subject: string;
	    body?: string;
	    files: FileDiff[];
	
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
	        this.files = this.convertValues(source["files"], FileDiff);
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
	export class ProjectIssue {
	    source: string;
	    key: string;
	    title: string;
	    body?: string;
	    status?: string;
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
	        this.author = source["author"];
	        this.url = source["url"];
	        this.isMine = source["isMine"];
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
	export class SkillInfo {
	    name: string;
	    description: string;
	    source: string;
	    path: string;
	    updatedAt: string;
	    sizeBytes: number;
	
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

export namespace jirax {
	
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
	    openCodeQuota: OpenCodeQuota;
	    providers: AIProviderUsage[];
	
	    static createFrom(source: any = {}) {
	        return new AIUsageReport(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.generatedAt = source["generatedAt"];
	        this.agyQuota = this.convertValues(source["agyQuota"], AgyQuota);
	        this.openCodeQuota = this.convertValues(source["openCodeQuota"], OpenCodeQuota);
	        this.providers = this.convertValues(source["providers"], AIProviderUsage);
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
	
	

}

export namespace orchestrator {
	
	export class Result {
	    SessionID: string;
	    Summary: string;
	    Branches: string[];
	    integrationBranch?: string;
	    PullRequests: githubx.PullRequest[];
	    inputTokens: number;
	    outputTokens: number;
	    totalTokens: number;
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
	        this.PullRequests = this.convertValues(source["PullRequests"], githubx.PullRequest);
	        this.inputTokens = source["inputTokens"];
	        this.outputTokens = source["outputTokens"];
	        this.totalTokens = source["totalTokens"];
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

export namespace team {
	
	export class Agent {
	    name: string;
	    role: string;
	    provider: string;
	    model: string;
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
	        this.instructions = source["instructions"];
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
	    auto_commit: boolean;
	    auto_pr: boolean;
	    homolog_branch?: string;
	    protected_branches?: string[];
	    require_main_merge_approval?: boolean;
	    max_qa_loops: number;
	    max_dynamic_devs?: number;
	    max_llm_calls: number;
	    max_tokens?: number;
	    in_turn_keep_tool_results?: number;
	    review_model?: string;
	    base_branch: string;
	    shell_allowlist?: string[];
	    confirm_shell?: boolean;
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
	        this.auto_commit = source["auto_commit"];
	        this.auto_pr = source["auto_pr"];
	        this.homolog_branch = source["homolog_branch"];
	        this.protected_branches = source["protected_branches"];
	        this.require_main_merge_approval = source["require_main_merge_approval"];
	        this.max_qa_loops = source["max_qa_loops"];
	        this.max_dynamic_devs = source["max_dynamic_devs"];
	        this.max_llm_calls = source["max_llm_calls"];
	        this.max_tokens = source["max_tokens"];
	        this.in_turn_keep_tool_results = source["in_turn_keep_tool_results"];
	        this.review_model = source["review_model"];
	        this.base_branch = source["base_branch"];
	        this.shell_allowlist = source["shell_allowlist"];
	        this.confirm_shell = source["confirm_shell"];
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

