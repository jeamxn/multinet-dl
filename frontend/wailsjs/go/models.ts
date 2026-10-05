export namespace main {
	
	export class AddRequest {
	    urls: string[];
	    fileName: string;
	    headers: string[];
	
	    static createFrom(source: any = {}) {
	        return new AddRequest(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.urls = source["urls"];
	        this.fileName = source["fileName"];
	        this.headers = source["headers"];
	    }
	}
	export class AppInfo {
	    version: string;
	    cli: string;
	    cliVersion: string;
	    cliError: string;
	    os: string;
	
	    static createFrom(source: any = {}) {
	        return new AppInfo(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.version = source["version"];
	        this.cli = source["cli"];
	        this.cliVersion = source["cliVersion"];
	        this.cliError = source["cliError"];
	        this.os = source["os"];
	    }
	}
	export class Config {
	    dir: string;
	    networks: string[];
	    conns: number;
	
	    static createFrom(source: any = {}) {
	        return new Config(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.dir = source["dir"];
	        this.networks = source["networks"];
	        this.conns = source["conns"];
	    }
	}
	export class NetProgress {
	    id: string;
	    label: string;
	    bytes: number;
	    speed: number;
	    active: number;
	    failed: boolean;
	    lastError?: string;
	
	    static createFrom(source: any = {}) {
	        return new NetProgress(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.label = source["label"];
	        this.bytes = source["bytes"];
	        this.speed = source["speed"];
	        this.active = source["active"];
	        this.failed = source["failed"];
	        this.lastError = source["lastError"];
	    }
	}
	export class Item {
	    id: string;
	    url: string;
	    fileName: string;
	    dir: string;
	    path: string;
	    networks: string[];
	    conns: number;
	    headers: string[];
	    state: string;
	    size: number;
	    done: number;
	    speed: number;
	    rangeOK: boolean;
	    throttled: number;
	    netStats: NetProgress[];
	    error: string;
	    warnings: string[];
	    createdAt: number;
	    finishedAt: number;
	    command: string;
	
	    static createFrom(source: any = {}) {
	        return new Item(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.url = source["url"];
	        this.fileName = source["fileName"];
	        this.dir = source["dir"];
	        this.path = source["path"];
	        this.networks = source["networks"];
	        this.conns = source["conns"];
	        this.headers = source["headers"];
	        this.state = source["state"];
	        this.size = source["size"];
	        this.done = source["done"];
	        this.speed = source["speed"];
	        this.rangeOK = source["rangeOK"];
	        this.throttled = source["throttled"];
	        this.netStats = this.convertValues(source["netStats"], NetProgress);
	        this.error = source["error"];
	        this.warnings = source["warnings"];
	        this.createdAt = source["createdAt"];
	        this.finishedAt = source["finishedAt"];
	        this.command = source["command"];
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
	export class NetInfo {
	    id: string;
	    index: number;
	    label: string;
	    kind: string;
	    virtual: boolean;
	    mac: string;
	    addrs: string[];
	
	    static createFrom(source: any = {}) {
	        return new NetInfo(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.index = source["index"];
	        this.label = source["label"];
	        this.kind = source["kind"];
	        this.virtual = source["virtual"];
	        this.mac = source["mac"];
	        this.addrs = source["addrs"];
	    }
	}
	
	export class TestResult {
	    id: string;
	    label: string;
	    ok: boolean;
	    ip?: string;
	    loc?: string;
	    ms?: number;
	    error?: string;
	
	    static createFrom(source: any = {}) {
	        return new TestResult(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.label = source["label"];
	        this.ok = source["ok"];
	        this.ip = source["ip"];
	        this.loc = source["loc"];
	        this.ms = source["ms"];
	        this.error = source["error"];
	    }
	}

}

