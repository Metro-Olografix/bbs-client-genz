export namespace main {
	
	export class BBSEntry {
	    name: string;
	    host: string;
	    port: number;
	
	    static createFrom(source: any = {}) {
	        return new BBSEntry(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.name = source["name"];
	        this.host = source["host"];
	        this.port = source["port"];
	    }
	}

}

