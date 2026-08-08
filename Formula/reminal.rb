class Reminal < Formula
  desc "Remote terminal access — secure, zero-config alternative to SSH"
  homepage "https://github.com/harshalgajjar/Reminal"
  version "2.1.0"
  license "AGPL-3.0-or-later"

  head do
    url "https://github.com/harshalgajjar/Reminal.git", branch: "main"
  end

  on_macos do
    on_arm do
      url "https://github.com/harshalgajjar/Reminal/releases/download/v2.1.0/reminal_2.1.0_darwin_arm64.tar.gz"
      sha256 "8786d52826cfeafa4cf56856bcbf58c85915ef57b56d20654f17499c6229b75c"
    end
    on_intel do
      url "https://github.com/harshalgajjar/Reminal/releases/download/v2.1.0/reminal_2.1.0_darwin_amd64.tar.gz"
      sha256 "4f8c6d9c37b5ec8cf1f9f8962af873690e9953bcd3181fb18bc1ae8cd629dd53"
    end
  end

  on_linux do
    on_arm do
      url "https://github.com/harshalgajjar/Reminal/releases/download/v2.1.0/reminal_2.1.0_linux_arm64.tar.gz"
      sha256 "7bf24b0a3c4fb8083d1d8643d34591da5af15d08625c7de5c718309900caf74f"
    end
    on_intel do
      url "https://github.com/harshalgajjar/Reminal/releases/download/v2.1.0/reminal_2.1.0_linux_amd64.tar.gz"
      sha256 "432c12d67780f109edf463be2d3793703bdbe3424a58ece23645030bc7198184"
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
