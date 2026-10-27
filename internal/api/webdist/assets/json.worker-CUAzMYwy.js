(function(){"use strict";self.onmessage=e=>{try{self.postMessage({ok:!0,value:JSON.parse(e.data)})}catch(s){self.postMessage({ok:!1,error:String(s)})}}})();
