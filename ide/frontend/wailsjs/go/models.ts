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
	    added: number;
	    removed: number;
	    lines: DiffLine[];
	
	    static createFrom(source: any = {}) {
	        return new FileDiff(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.path = source["path"];
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
	export class GitHubStatusInfo {
	    installed: boolean;
	    loggedIn: boolean;
	    user: string;
	    remote: string;
	    detail: string;
	
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
	    defaultModel: string;
	    agentModels?: Record<string, string>;
	    worktrees: boolean;
	    autoCommit: boolean;
	    autoPR: boolean;
	    jiraEnabled: boolean;
	    jiraProject: string;
	    baseBranch: string;
	    maxTokens: number;
	
	    static createFrom(source: any = {}) {
	        return new WizardInput(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.teamName = source["teamName"];
	        this.chiefRole = source["chiefRole"];
	        this.devCount = source["devCount"];
	        this.defaultModel = source["defaultModel"];
	        this.agentModels = source["agentModels"];
	        this.worktrees = source["worktrees"];
	        this.autoCommit = source["autoCommit"];
	        this.autoPR = source["autoPR"];
	        this.jiraEnabled = source["jiraEnabled"];
	        this.jiraProject = source["jiraProject"];
	        this.baseBranch = source["baseBranch"];
	        this.maxTokens = source["maxTokens"];
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

}

export namespace modelx {
	
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

}

export namespace orchestrator {
	
	export class Result {
	    SessionID: string;
	    Summary: string;
	    Branches: string[];
	    PullRequests: githubx.PullRequest[];
	    inputTokens: number;
	    outputTokens: number;
	    totalTokens: number;
	
	    static createFrom(source: any = {}) {
	        return new Result(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.SessionID = source["SessionID"];
	        this.Summary = source["Summary"];
	        this.Branches = source["Branches"];
	        this.PullRequests = this.convertValues(source["PullRequests"], githubx.PullRequest);
	        this.inputTokens = source["inputTokens"];
	        this.outputTokens = source["outputTokens"];
	        this.totalTokens = source["totalTokens"];
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
	export class Settings {
	    jira_enabled: boolean;
	    jira_project?: string;
	    worktrees?: boolean;
	    auto_commit: boolean;
	    auto_pr: boolean;
	    max_qa_loops: number;
	    max_llm_calls: number;
	    max_tokens?: number;
	    base_branch: string;
	    shell_allowlist?: string[];
	    confirm_shell?: boolean;
	
	    static createFrom(source: any = {}) {
	        return new Settings(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.jira_enabled = source["jira_enabled"];
	        this.jira_project = source["jira_project"];
	        this.worktrees = source["worktrees"];
	        this.auto_commit = source["auto_commit"];
	        this.auto_pr = source["auto_pr"];
	        this.max_qa_loops = source["max_qa_loops"];
	        this.max_llm_calls = source["max_llm_calls"];
	        this.max_tokens = source["max_tokens"];
	        this.base_branch = source["base_branch"];
	        this.shell_allowlist = source["shell_allowlist"];
	        this.confirm_shell = source["confirm_shell"];
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

