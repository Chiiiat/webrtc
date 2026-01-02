> 声明：  
> 此为个人创建的一个 webrtc 分支版本，其中 webrtc 项目许可协议遵循 MIT 协议     
> 此分支中我添加了内联注释，仅用于学习目的。所有原始的版权和许可声明均保留不变      
> 最后非常感谢 Pion 开源    


此部分仅做阅读痕迹记录：    

    ./examples/data-channels-simple
    ./examples/play-from-disk

Model {     

    ✅ ./peerconnection.go   
    ✅ ./signalingstate.go   
    总结：{

    }   

    Media:  
        ✅ ./rtptransceiver.go   
        ✅ ./rtpsender.go / ./rtpreceiver.go   
        ✅ ./track_local.go / ./track_remote.go    
        ✅ ./mediaengine.go  
        总结：{

    }   

    DataChannel:    
        ✅ ./datachannel.go  
        ✅ ./datachannelinit.go, ./datachannelparameters.go, ./datachannelstate.go   
        总结：{

    }   

    SDP:    
        ✅ ./sessiondescription.go   
        ✅ ./sdp.go    
        总结：{

    }     
}
 
Transport {

    ICE:
        ✅ ./icegatherer.go    
        ✅ ./icetransport.go   
        ✅ ./icecandidate.go / ./icecandidatepair.go / ./iceserver.go 
        总结：{

    }   

    DTLS + SRTP:    
        ./dtlstransport.go  
        ./dtlsparameters.go / ./dtlsrole.go / ./dtlstransportstate.go   
        总结：{

    }   

    SCTP:   
        ./sctptransport.go    
        ./sctpcapabilities.go / ./sctptransportstate.go
        总结：{

    }   

    RTP / RTCP / Code / Decode: 
        ./rtpcodec.go、./rtpcapabilities.go 
        ./rtpsendparameters.go / ./rtpreceiveparameters.go
        总结：{

    }   
}
    