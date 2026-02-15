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
	export class ScreenCell {
	    ch: string;
	    fgR: number;
	    fgG: number;
	    fgB: number;
	    bgR: number;
	    bgG: number;
	    bgB: number;
	    bold: boolean;
	    ul: boolean;
	    blink: boolean;
	    rev: boolean;
	
	    static createFrom(source: any = {}) {
	        return new ScreenCell(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.ch = source["ch"];
	        this.fgR = source["fgR"];
	        this.fgG = source["fgG"];
	        this.fgB = source["fgB"];
	        this.bgR = source["bgR"];
	        this.bgG = source["bgG"];
	        this.bgB = source["bgB"];
	        this.bold = source["bold"];
	        this.ul = source["ul"];
	        this.blink = source["blink"];
	        this.rev = source["rev"];
	    }
	}
	export class ScreenSnapshot {
	    cells: ScreenCell[][];
	    cursorX: number;
	    cursorY: number;
	
	    static createFrom(source: any = {}) {
	        return new ScreenSnapshot(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.cells = this.convertValues(source["cells"], ScreenCell);
	        this.cursorX = source["cursorX"];
	        this.cursorY = source["cursorY"];
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

