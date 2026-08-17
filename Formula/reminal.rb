class Reminal < Formula
  desc "Remote terminal access — secure, zero-config alternative to SSH"
  homepage "https://github.com/harshalgajjar/Reminal"
  version "3.0.4"
  license "AGPL-3.0-or-later"

  head do
    url "https://github.com/harshalgajjar/Reminal.git", branch: "main"
  end

  on_macos do
    on_arm do
      url "https://github.com/harshalgajjar/Reminal/releases/download/v3.0.4/reminal_3.0.4_darwin_arm64.tar.gz"
      sha256 "aac9686da84a96aa959d0de501f07e10e9f504fb0865bdc840c594ec802aff9e"
    end
    on_intel do
      url "https://github.com/harshalgajjar/Reminal/releases/download/v3.0.4/reminal_3.0.4_darwin_amd64.tar.gz"
      sha256 "5a29088ac0bd1ffcb49ce405421bf39ecf9203a5b7442af39de7b18b94846994"
    end
  end

  on_linux do
    on_arm do
      url "https://github.com/harshalgajjar/Reminal/releases/download/v3.0.4/reminal_3.0.4_linux_arm64.tar.gz"
      sha256 "3c853ff187df2403b971be80db159c5fcb3b06b434678c0128f5b085f7666d3c"
    end
    on_intel do
      url "https://github.com/harshalgajjar/Reminal/releases/download/v3.0.4/reminal_3.0.4_linux_amd64.tar.gz"
      sha256 "d7cc0f5f1b918cd691acf57f891752eb20c36b8c73e9b1b838bb259fcc75fda2"
    end
  end

  depends_on "go" => :build if build.head?

  def install
    if build.head?
      system "go", "build", "-ldflags=#{ldflags}", "-o", bin/"reminal", "./cmd/reminal"
      # Build the native window-capture helper from source when Xcode is present;
      # otherwise the window mirror falls back to screencapture.
      if OS.mac? && which("swiftc")
        system "swiftc", "-O", "-o", bin/"reminal-capture", "native/reminal-capture/main.swift"
      end
    else
      bin.install "reminal"
      # The darwin bottle bundles the ScreenCaptureKit capture helper next to the
      # binary; the agent auto-discovers it for the native window mirror.
      bin.install "reminal-capture" if File.exist?("reminal-capture")
    end
  end

  def ldflags
    "-s -w " \
      "-X main.version=#{version} " \
      "-X github.com/reminal/reminal/internal/config.DefaultCloudRelay=wss://reminal-relay.futuristic.workers.dev/ws " \
      "-X github.com/reminal/reminal/internal/config.DefaultCloudWeb=https://reminal-relay.futuristic.workers.dev"
  end

  def caveats
    <<~EOS
      reminal connects to the hosted relay automatically — no setup needed.

        reminal              # share your terminal
        reminal --connect ID --pin PIN
    EOS
  end

  test do
    assert_match version.to_s, shell_output("#{bin}/reminal version")
  end
end
